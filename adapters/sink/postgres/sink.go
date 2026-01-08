package postgres

import (
	"context"
	_ "embed"
	"fmt"
	"os"
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
  block_hash = EXCLUDED.block_hash;
`

func NewSink(cfg SinkConfig) (*PGSink, error) {
	if cfg.Pool == nil {
		return nil, fmt.Errorf("pool is required")
	}
	if cfg.Handler == nil {
		return nil, fmt.Errorf("handler is required")
	}

	m := cfg.Metrics
	if m == nil {
		m = metrics.Noop{} 
	}

	// the performance after 32 rows with copy will show improvement
	if cfg.CopyThreshold <= 0 {
		cfg.CopyThreshold = DefaultCopyThreshold
	}

	pgSink := &PGSink{db: cfg.Pool, handler: cfg.Handler, copyThreshold: cfg.CopyThreshold, metrics: m}
	if err := pgSink.initInternalSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("init internal schema: %w", err)
	}
	return pgSink, nil
}

func (s *PGSink) Store(ctx context.Context, events []types.Event) (err error) {
	if len(events) == 0 {
		return nil
	}

	start := time.Now()
	chainId := events[0].ChainId
	success := false
	defer func() {
		d := time.Since(start)
		s.metrics.ObservedSinkWriteDuration(chainId, d, success)
		if !success && err != nil {
			s.metrics.IncSinkErrors(chainId)
		}
	}()

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	useCopy := len(events) >= s.copyThreshold

	if useCopy {
		err = s.copyInternalEvents(ctx, tx, events)
	} else {
		err = s.insertInternalEvents(ctx, tx, events)
	}
	if err != nil {
		return err
	}

	if s.handler != nil {
		for i, event := range events {
			if err = s.handler.Handle(ctx, tx, event); err != nil {
				return fmt.Errorf("handler failed for event %d (%s): %w", i, event.Id, err)
			}
		}
	}

	_, err = tx.Exec(ctx, upsertCursorSQL, events[len(events)-1].ChainId, events[len(events)-1].BlockNumber, events[len(events)-1].BlockHash)
	if err != nil {
		return fmt.Errorf("failed to update chronicle_cursors: %w", err)
	}

	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	success = true

	// Metrics to measure sink writes
	s.metrics.IncSinkWrites(chainId, uint64(len(events)))

	// Metrics to set persisted height
	s.metrics.SetIndexedHeight(chainId, events[len(events)-1].BlockNumber)

	// Metrics to measure how many persisted have been processed
	blocks := make(map[uint64]struct{}, len(events))
	for _, ev := range events {
		blocks[ev.BlockNumber] = struct{}{}
	}
	s.metrics.IncBlocksProcessed(chainId, uint64(len(blocks)))

	return nil
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
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

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

	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

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
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

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
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err = tx.Exec(ctx, internalSchemaSQL); err != nil {
		return fmt.Errorf("exec internal schema: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit internal schema: %w", err)
	}
	return nil
}

func (s *PGSink) copyInternalEvents(ctx context.Context, tx pgx.Tx, events []types.Event) error {
	rows := make([][]interface{}, len(events))
	for i, event := range events {
		rows[i] = []interface{}{
			event.Id,
			event.ChainId,
			event.EventType,
			event.BlockNumber,
			event.BlockHash,
			event.TransactionHash,
			event.LogIndex,
			event.Address,
			int64(event.Timestamp),
			event.Fields,
		}
	}

	_, err := tx.CopyFrom(ctx,
		pgx.Identifier{"chronicle_events"},
		[]string{"event_id", "chain_id", "kind", "block_num", "block_hash",
			"tx_hash", "log_index", "address", "ts", "payload"},
		pgx.CopyFromRows(rows))

	if err != nil {
		return fmt.Errorf("copy from chronicle_events: %w", err)
	}

	return nil
}

func (s *PGSink) insertInternalEvents(ctx context.Context, tx pgx.Tx, events []types.Event) error {
	for _, event := range events {
		_, err := tx.Exec(ctx, `
            INSERT INTO chronicle_events (event_id, chain_id, kind, block_num, block_hash, 
            tx_hash, log_index, address, ts, payload)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
            ON CONFLICT (event_id) DO NOTHING;
            `, event.Id,
			event.ChainId,
			event.EventType,
			event.BlockNumber,
			event.BlockHash,
			event.TransactionHash,
			event.LogIndex,
			event.Address,
			int64(event.Timestamp),
			event.Fields,
		)

		if err != nil {
			return err
		}
	}

	return nil
}
