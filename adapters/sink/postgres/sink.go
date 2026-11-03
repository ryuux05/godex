package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/pkg/core/errors"
	"github.com/ryuux05/godex/pkg/core/metrics"
	"github.com/ryuux05/godex/pkg/core/types"
)

type SinkConfig struct {
	Pool          *pgxpool.Pool
	Handler       Handler
	CopyThreshold int
	Metrics       metrics.Metrics
}

type PGSink struct {
	db            *pgxpool.Pool
	handler       Handler
	copyThreshold int
	metrics       metrics.Metrics
}

const (
	// threshold to switch to COPY for bulk inserting
	DefaultCopyThreshold = 32
)

const upsertCursorSQL = `
INSERT INTO chronicle_cursors (chain_id, block_num, block_hash)
VALUES ($1, $2, $3)
ON CONFLICT (chain_id)
DO UPDATE SET
  block_num = EXCLUDED.block_num,
  block_hash = EXCLUDED.block_hash
`

func NewSink(cfg SinkConfig) (*PGSink, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return NewSinkContext(ctx, cfg)
}

// NewSinkContext initializes internal tables within the caller's startup budget.
func NewSinkContext(ctx context.Context, cfg SinkConfig) (*PGSink, error) {
	if cfg.Pool == nil {
		return nil, fmt.Errorf("pool is required")
	}
	if nilComponent(cfg.Handler) {
		return nil, fmt.Errorf("handler is required")
	}
	if validator, ok := cfg.Handler.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return nil, fmt.Errorf("handler: %w", err)
		}
	}

	m := cfg.Metrics
	if nilComponent(m) {
		m = metrics.Noop{}
	}

	// the performance after 32 rows with copy will show improvement
	if cfg.CopyThreshold <= 0 {
		cfg.CopyThreshold = DefaultCopyThreshold
	}

	pgSink := &PGSink{db: cfg.Pool, handler: cfg.Handler, copyThreshold: cfg.CopyThreshold, metrics: m}
	if err := pgSink.initInternalSchema(ctx); err != nil {
		return nil, fmt.Errorf("init internal schema: %w", err)
	}
	return pgSink, nil
}

func nilComponent(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		return v.IsNil()
	}
	return false
}

func (s *PGSink) Store(ctx context.Context, events []types.Event) error {
	if len(events) == 0 {
		return nil
	}
	last := events[len(events)-1]
	return s.StoreWindow(ctx, last.ChainId, last.BlockNumber, last.BlockHash, events)
}

// StoreWindow commits handler effects, newly inserted events, and the processed
// window cursor together. Replayed IDs never invoke handlers again.
func (s *PGSink) StoreWindow(ctx context.Context, chainID string, toBlock uint64, blockHash string, events []types.Event) (err error) {
	if err := validateWindow(chainID, toBlock, blockHash, events); err != nil {
		return err
	}
	start := time.Now()
	success := false
	defer func() {
		s.metrics.ObservedSinkWriteDuration(chainID, time.Since(start), success)
		if !success && err != nil {
			s.metrics.IncSinkErrors(chainID)
		}
	}()
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer rollbackTransaction(tx)

	var inserted []types.Event
	if len(events) >= s.copyThreshold && len(events) > 0 {
		inserted, err = s.copyInternalEvents(ctx, tx, events)
	} else {
		inserted, err = s.insertInternalEvents(ctx, tx, events)
	}
	if err != nil {
		return err
	}
	for i, event := range inserted {
		if err = s.handler.Handle(ctx, tx, event); err != nil {
			return fmt.Errorf("handler failed for event %d (%s): %w", i, event.Id, err)
		}
	}
	// Late replay of an older window must not move the durable cursor backward.
	_, err = tx.Exec(ctx, upsertCursorSQL+" WHERE chronicle_cursors.block_num < EXCLUDED.block_num OR (chronicle_cursors.block_num = EXCLUDED.block_num AND chronicle_cursors.block_hash = EXCLUDED.block_hash)", chainID, toBlock, blockHash)
	if err != nil {
		return fmt.Errorf("failed to update chronicle_cursors: %w", err)
	}
	var durableHeight uint64
	var durableHash string
	if err = tx.QueryRow(ctx, "SELECT block_num,block_hash FROM chronicle_cursors WHERE chain_id=$1", chainID).Scan(&durableHeight, &durableHash); err != nil {
		return fmt.Errorf("read committed cursor: %w", err)
	}
	if durableHeight == toBlock && durableHash != blockHash {
		return fmt.Errorf("conflicting cursor hash at block %d; rollback before changing canonical history", toBlock)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	success = true
	s.metrics.IncSinkWrites(chainID, uint64(len(inserted)))
	s.metrics.SetIndexedHeight(chainID, durableHeight)
	blocks := make(map[uint64]struct{}, len(inserted))
	for _, event := range inserted {
		blocks[event.BlockNumber] = struct{}{}
	}
	s.metrics.IncBlocksProcessed(chainID, uint64(len(blocks)))
	return nil
}

func validateWindow(chainID string, toBlock uint64, blockHash string, events []types.Event) error {
	if chainID == "" || blockHash == "" || toBlock > math.MaxInt64 {
		return fmt.Errorf("a chain ID, block hash and SQL-representable window height are required")
	}
	for i, event := range events {
		if event.Id == "" || event.ChainId != chainID || event.BlockHash == "" {
			return fmt.Errorf("event %d has missing identity or a different chain", i)
		}
		if event.BlockNumber > toBlock || event.Timestamp > math.MaxInt64 || event.LogIndex > math.MaxInt32 {
			return fmt.Errorf("event %d exceeds its window or SQL integer bounds", i)
		}
		if event.BlockNumber == toBlock && event.BlockHash != blockHash {
			return fmt.Errorf("event %d hash disagrees with window cursor", i)
		}
		if i > 0 && (event.BlockNumber < events[i-1].BlockNumber || (event.BlockNumber == events[i-1].BlockNumber && event.LogIndex < events[i-1].LogIndex)) {
			return fmt.Errorf("events must be ordered by block number and log index")
		}
		if event.Fields == nil {
			return fmt.Errorf("event %d fields cannot be nil", i)
		}
		if _, err := json.Marshal(event.Fields); err != nil {
			return fmt.Errorf("event %d has invalid fields: %w", i, err)
		}
	}
	return nil
}

// Always release transactions, including panic and canceled request paths.
// Rollback after Commit is harmless; use an independent bounded cleanup context.
func rollbackTransaction(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func (s *PGSink) Rollback(ctx context.Context, chainId string, toBlock uint64, blockHash string) (err error) {
	start := time.Now()
	success := false
	defer func() {
		s.metrics.ObservedSinkWriteDuration(chainId, time.Since(start), success)
		if !success && err != nil {
			s.metrics.IncSinkErrors(chainId)
		}
	}()

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})

	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer rollbackTransaction(tx)

	if handler, ok := s.handler.(RollbackHandler); ok {
		if err := handler.Rollback(ctx, tx, chainId, toBlock); err != nil {
			return fmt.Errorf("handler rollback: %w", err)
		}
	}
	// Delete all events from toBlock to current block
	_, err = tx.Exec(ctx, `
        DELETE FROM chronicle_events
        WHERE chain_id = $1 AND block_num >= $2
    `, chainId, toBlock)
	if err != nil {
		return fmt.Errorf("failed to delete events: %w", err)
	}
	newBlock := uint64(0)
	if toBlock > 0 {
		newBlock = toBlock - 1
	}

	// Update cursor to rollback point
	_, err = tx.Exec(ctx, upsertCursorSQL, chainId, newBlock, blockHash)
	if err != nil {
		return fmt.Errorf("failed to update cursor: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rollback: %w", err)
	}

	success = true

	// Metrics to set new indexedheight after rollback
	s.metrics.SetIndexedHeight(chainId, newBlock)

	return nil
}

func (s *PGSink) LoadCursor(ctx context.Context, chainId string) (blockNum uint64, blockHash string, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT block_num, block_hash
		FROM chronicle_cursors
		WHERE chain_id = $1;
	`, chainId).Scan(&blockNum, &blockHash)

	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, "", errors.ErrCursorNotFound
		}
		return 0, "", fmt.Errorf("load cursor: %w", err)
	}

	return blockNum, blockHash, nil
}

func (s *PGSink) UpdateCursor(ctx context.Context, chainId string, newBlock uint64, blockHash string) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer rollbackTransaction(tx)

	_, err = tx.Exec(ctx, upsertCursorSQL, chainId, newBlock, blockHash)

	if err != nil {
		return fmt.Errorf("failed to update cursor: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rollback: %w", err)
	}

	// Metrics to set new indexedheight after rollback
	s.metrics.SetIndexedHeight(chainId, newBlock)

	return nil
}

func (s *PGSink) Migrate(ctx context.Context, sqlString string) error {
	if sqlString == "" {
		return fmt.Errorf("sql string cannot be empty")
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer rollbackTransaction(tx)

	_, err = tx.Exec(ctx, sqlString)
	if err != nil {
		return fmt.Errorf("migration execution failed: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}

	fmt.Printf("Migration executed successfully")
	return nil
}

func (s *PGSink) MigrateWithFile(ctx context.Context, filePath string) error {
	if filePath == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	sqlString, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read schema file: %w", err)
	}

	return s.Migrate(ctx, string(sqlString))
}

//go:embed schema_internal.sql
var internalSchemaSQL string

func (s *PGSink) initInternalSchema(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin internal schema tx: %w", err)
	}
	defer rollbackTransaction(tx)

	if _, err = tx.Exec(ctx, internalSchemaSQL); err != nil {
		return fmt.Errorf("exec internal schema: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit internal schema: %w", err)
	}
	return nil
}

var eventColumns = []string{"event_id", "chain_id", "kind", "block_num", "block_hash", "tx_hash", "log_index", "address", "ts", "payload"}

func (s *PGSink) copyInternalEvents(ctx context.Context, tx pgx.Tx, events []types.Event) ([]types.Event, error) {
	// Stage without unique indexes so duplicate IDs within a batch are harmless.
	if _, err := tx.Exec(ctx, "CREATE TEMP TABLE godex_events_stage (LIKE chronicle_events INCLUDING DEFAULTS) ON COMMIT DROP"); err != nil {
		return nil, fmt.Errorf("create COPY staging table: %w", err)
	}
	rows := make([][]any, len(events))
	for i, event := range events {
		rows[i] = eventRow(event)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"pg_temp", "godex_events_stage"}, eventColumns, pgx.CopyFromRows(rows)); err != nil {
		return nil, fmt.Errorf("copy staged events: %w", err)
	}
	rowsResult, err := tx.Query(ctx, `
		INSERT INTO chronicle_events (event_id, chain_id, kind, block_num, block_hash, tx_hash, log_index, address, ts, payload)
		SELECT event_id, chain_id, kind, block_num, block_hash, tx_hash, log_index, address, ts, payload
		FROM pg_temp.godex_events_stage
		ON CONFLICT (event_id) DO NOTHING
		RETURNING event_id`)
	if err != nil {
		return nil, fmt.Errorf("merge staged events: %w", err)
	}
	ids, err := pgx.CollectRows(rowsResult, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	newIDs := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		newIDs[id] = struct{}{}
	}
	inserted := make([]types.Event, 0, len(ids))
	for _, event := range events {
		if _, ok := newIDs[event.Id]; ok {
			inserted = append(inserted, event)
			delete(newIDs, event.Id)
		}
	}
	return inserted, nil
}

func eventRow(event types.Event) []any {
	return []any{event.Id, event.ChainId, event.EventType, event.BlockNumber, event.BlockHash, event.TransactionHash, event.LogIndex, event.Address, int64(event.Timestamp), event.Fields}
}

func (s *PGSink) insertInternalEvents(ctx context.Context, tx pgx.Tx, events []types.Event) ([]types.Event, error) {
	inserted := make([]types.Event, 0, len(events))
	for _, event := range events {
		tag, err := tx.Exec(ctx, `
			INSERT INTO chronicle_events (event_id, chain_id, kind, block_num, block_hash, tx_hash, log_index, address, ts, payload)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (event_id) DO NOTHING`, eventRow(event)...)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() > 0 {
			inserted = append(inserted, event)
		}
	}
	return inserted, nil
}
