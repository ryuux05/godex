// Package projections manages the example handlers' application schemas.
package projections

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/adapters/sink/postgres"
	"github.com/ryuux05/godex/pkg/core/types"
)

type Config struct {
	SQL string
	// Tables lists handler-owned tables in dependency order for an explicit rebuild.
	Tables                                 []string
	ProbeTable, RequiredColumn             string
	Handler                                postgres.Handler
	Rebuild                                bool
	IntegerFields, BytesFields, EventTypes []string
}

// Prepare creates a fresh schema or explicitly rebuilds projections from stored
// canonical events. Legacy schemas fail with an actionable error until a rebuild
// is requested. Internal events and indexing cursors are never reset.
func Prepare(ctx context.Context, pool *pgxpool.Pool, cfg Config) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var legacy bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=$1)
		AND NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2)`, cfg.ProbeTable, cfg.RequiredColumn).Scan(&legacy)
	if err != nil {
		return err
	}
	if legacy && !cfg.Rebuild {
		return fmt.Errorf("legacy %s schema requires an explicit projection rebuild: set REBUILD_PROJECTIONS=1 for one startup", cfg.ProbeTable)
	}
	if cfg.Rebuild {
		for _, table := range cfg.Tables {
			if _, err := tx.Exec(ctx, "DROP TABLE IF EXISTS "+pgx.Identifier{table}.Sanitize()); err != nil {
				return fmt.Errorf("reset projection %s: %w", table, err)
			}
		}
	}
	if _, err = tx.Exec(ctx, cfg.SQL); err != nil {
		return fmt.Errorf("application schema: %w", err)
	}
	if cfg.Rebuild {
		// Read through bounded keyset pages. Rows must be closed before invoking a
		// handler on the same transaction/connection.
		lastBlock, lastIndex := int64(-1), int64(-1)
		lastChain, lastID := "", ""
		for {
			rows, err := tx.Query(ctx, `SELECT event_id,chain_id,kind,block_num,block_hash,tx_hash,log_index,address,ts,payload FROM chronicle_events
				WHERE kind=ANY($5::text[]) AND (block_num,log_index,chain_id,event_id)>($1::bigint,$2::int,$3::text,$4::text)
				ORDER BY block_num,log_index,chain_id,event_id LIMIT 256`, lastBlock, lastIndex, lastChain, lastID, cfg.EventTypes)
			if err != nil {
				return err
			}
			type stored struct {
				event   types.Event
				payload []byte
			}
			batch, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (stored, error) {
				var s stored
				err := row.Scan(&s.event.Id, &s.event.ChainId, &s.event.EventType, &s.event.BlockNumber, &s.event.BlockHash, &s.event.TransactionHash, &s.event.LogIndex, &s.event.Address, &s.event.Timestamp, &s.payload)
				return s, err
			})
			if err != nil {
				return err
			}
			if len(batch) == 0 {
				break
			}
			for _, s := range batch {
				fields, err := restoreFields(s.payload, cfg.IntegerFields, cfg.BytesFields)
				if err != nil {
					return fmt.Errorf("restore event %s: %w", s.event.Id, err)
				}
				s.event.Fields = fields
				if err := cfg.Handler.Handle(ctx, tx, s.event); err != nil {
					return fmt.Errorf("rebuild event %s: %w", s.event.Id, err)
				}
			}
			last := batch[len(batch)-1].event
			lastBlock, lastIndex, lastChain, lastID = int64(last.BlockNumber), int64(last.LogIndex), last.ChainId, last.Id
		}
	}
	return tx.Commit(ctx)
}

func restoreFields(payload []byte, integerFields, bytesFields []string) (types.EventFields, error) {
	d := json.NewDecoder(strings.NewReader(string(payload)))
	d.UseNumber()
	var fields types.EventFields
	if err := d.Decode(&fields); err != nil {
		return nil, err
	}
	for _, name := range integerFields {
		value, exists := fields[name]
		if !exists || value == nil {
			continue
		}
		text := fmt.Sprint(value)
		n, ok := new(big.Int).SetString(text, 10)
		if !ok {
			return nil, fmt.Errorf("invalid integer %s", name)
		}
		fields[name] = n
	}
	for _, name := range bytesFields {
		value, exists := fields[name]
		if !exists || value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("invalid bytes %s", name)
		}
		if strings.HasPrefix(text, "0x") {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, fmt.Errorf("invalid bytes %s: %w", name, err)
		}
		fields[name] = b
	}
	return fields, nil
}
