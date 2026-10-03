package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	coreerrors "github.com/ryuux05/godex/pkg/core/errors"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type handlerFunc func(context.Context, pgx.Tx, types.Event) error

func (f handlerFunc) Handle(ctx context.Context, tx pgx.Tx, event types.Event) error {
	return f(ctx, tx, event)
}

func TestSinkValidationWithoutDatabase(t *testing.T) {
	_, err := NewSink(SinkConfig{Handler: &mockHandler{}})
	assert.ErrorContains(t, err, "pool is required")
	_, err = NewSink(SinkConfig{Pool: &pgxpool.Pool{}})
	assert.ErrorContains(t, err, "handler is required")
	// These no-op and validation paths should be usable without a connection.
	s := &PGSink{}
	assert.NoError(t, s.Store(context.Background(), nil))
	assert.ErrorContains(t, s.Migrate(context.Background(), ""), "sql string cannot be empty")
	assert.ErrorContains(t, s.MigrateWithFile(context.Background(), ""), "file path cannot be empty")
	assert.ErrorContains(t, s.MigrateWithFile(context.Background(), "/missing/godex/schema.sql"), "failed to read schema file")
}

func testEvent(id, chain string, n uint64) types.Event {
	return types.Event{Id: id, ChainId: chain, EventType: "Transfer", BlockNumber: n, BlockHash: "hash-" + id, TransactionHash: "tx-" + id, Address: "0xabc", Fields: types.EventFields{"value": "42"}}
}

func TestStoreHandlerFailureIsAtomic(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(map[int]string{1: "COPY", 100: "INSERT"}[threshold], func(t *testing.T) {
			pool := getTestDB(t)
			ctx := context.Background()
			failure := errors.New("handler failure")
			handler := handlerFunc(func(ctx context.Context, tx pgx.Tx, event types.Event) error {
				if _, err := tx.Exec(ctx, "INSERT INTO handler_events (event_id) VALUES ($1)", event.Id); err != nil {
					return err
				}
				if event.Id == "second" {
					return failure
				}
				return nil
			})
			s, err := NewSink(SinkConfig{Pool: pool, Handler: handler, CopyThreshold: threshold})
			require.NoError(t, err)
			require.NoError(t, s.Migrate(ctx, "CREATE TABLE handler_events (event_id TEXT PRIMARY KEY)"))
			require.NoError(t, s.UpdateCursor(ctx, "1", 5, "hash-5"))
			err = s.Store(ctx, []types.Event{testEvent("first", "1", 6), testEvent("second", "1", 7)})
			assert.ErrorIs(t, err, failure)
			for _, table := range []string{"chronicle_events", "handler_events"} {
				var count int
				require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count))
				assert.Zero(t, count)
			}
			n, h, err := s.LoadCursor(ctx, "1")
			require.NoError(t, err)
			assert.Equal(t, uint64(5), n)
			assert.Equal(t, "hash-5", h)
		})
	}
}

func TestRollbackIsInclusiveAndIsolatedByChain(t *testing.T) {
	pool := getTestDB(t)
	s, err := NewSink(SinkConfig{Pool: pool, Handler: &mockHandler{}})
	require.NoError(t, err)
	ctx := context.Background()
	for _, chain := range []string{"1", "10"} {
		require.NoError(t, s.Store(ctx, []types.Event{testEvent(chain+"-4", chain, 4), testEvent(chain+"-5", chain, 5), testEvent(chain+"-6", chain, 6)}))
	}
	require.NoError(t, s.Rollback(ctx, "1", 5, "hash-1-4"))
	var ids []string
	rows, err := pool.Query(ctx, "SELECT event_id FROM chronicle_events ORDER BY event_id")
	require.NoError(t, err)
	ids, err = pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1-4", "10-4", "10-5", "10-6"}, ids)
	n, h, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(4), n)
	assert.Equal(t, "hash-1-4", h)
	n, _, err = s.LoadCursor(ctx, "10")
	require.NoError(t, err)
	assert.Equal(t, uint64(6), n)
}

func TestCursorMissingAndUpdates(t *testing.T) {
	pool := getTestDB(t)
	s, err := NewSink(SinkConfig{Pool: pool, Handler: &mockHandler{}})
	require.NoError(t, err)
	ctx := context.Background()
	_, _, err = s.LoadCursor(ctx, "missing")
	assert.ErrorIs(t, err, coreerrors.ErrCursorNotFound)
	for _, n := range []uint64{10, 20, 0} {
		require.NoError(t, s.UpdateCursor(ctx, "1", n, "hash"))
		got, h, err := s.LoadCursor(ctx, "1")
		require.NoError(t, err)
		assert.Equal(t, n, got)
		assert.Equal(t, "hash", h)
	}
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM chronicle_events").Scan(&count))
	assert.Zero(t, count)
}

func TestMigrateFailureRollsBackEarlierStatements(t *testing.T) {
	pool := getTestDB(t)
	s, err := NewSink(SinkConfig{Pool: pool, Handler: &mockHandler{}})
	require.NoError(t, err)
	ctx := context.Background()
	err = s.Migrate(ctx, "CREATE TABLE partial_migration (id INT); SELECT * FROM missing_table;")
	assert.ErrorContains(t, err, "migration execution failed")
	var exists bool
	require.NoError(t, pool.QueryRow(ctx, "SELECT to_regclass('partial_migration') IS NOT NULL").Scan(&exists))
	assert.False(t, exists)
}

func TestStoreInsertReplayIsIdempotent(t *testing.T) {
	pool := getTestDB(t)
	s, err := NewSink(SinkConfig{Pool: pool, Handler: &mockHandler{}, CopyThreshold: 100})
	require.NoError(t, err)
	ctx := context.Background()
	events := []types.Event{testEvent("one", "1", 1)}
	require.NoError(t, s.Store(ctx, events))
	require.NoError(t, s.Store(ctx, events))
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM chronicle_events").Scan(&count))
	assert.Equal(t, 1, count)
}

func TestStoreCopyDuplicateRollsBackBatch(t *testing.T) {
	pool := getTestDB(t)
	s, err := NewSink(SinkConfig{Pool: pool, Handler: &mockHandler{}, CopyThreshold: 1})
	require.NoError(t, err)
	ctx := context.Background()
	event := testEvent("original", "1", 1)
	require.NoError(t, s.Store(ctx, []types.Event{event}))
	// COPY currently rejects replayed IDs. Even on that failure, a new event
	// in the same batch must not persist or advance the cursor.
	err = s.Store(ctx, []types.Event{event, testEvent("new", "1", 2)})
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23505", pgErr.Code)
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM chronicle_events").Scan(&count))
	assert.Equal(t, 1, count)
	n, h, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), n)
	assert.Equal(t, event.BlockHash, h)
}

func TestRollbackCursorFailureRestoresDeletedEvents(t *testing.T) {
	pool := getTestDB(t)
	s, err := NewSink(SinkConfig{Pool: pool, Handler: &mockHandler{}})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, s.Store(ctx, []types.Event{testEvent("one", "1", 1), testEvent("two", "1", 2)}))
	require.NoError(t, s.Migrate(ctx, "ALTER TABLE chronicle_cursors ADD CHECK (block_num > 0)"))
	assert.ErrorContains(t, s.Rollback(ctx, "1", 1, ""), "failed to update cursor")
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM chronicle_events").Scan(&count))
	assert.Equal(t, 2, count, "a failed cursor update must roll back the preceding DELETE")
	n, h, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(2), n)
	assert.Equal(t, "hash-two", h)
	assert.Error(t, s.UpdateCursor(ctx, "1", 0, ""))
	n, _, err = s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(2), n)
}
