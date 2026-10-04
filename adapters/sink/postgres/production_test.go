package postgres

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplayAcrossInsertAndCopyThreshold(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()
	h := handlerFunc(func(ctx context.Context, tx pgx.Tx, e types.Event) error {
		_, err := tx.Exec(ctx, "INSERT INTO effects VALUES ($1)", e.Id)
		return err
	})
	s, err := NewSink(SinkConfig{Pool: pool, Handler: h, CopyThreshold: 2})
	require.NoError(t, err)
	require.NoError(t, s.Migrate(ctx, "CREATE TABLE effects (id TEXT PRIMARY KEY)"))
	a, b := testEvent("a", "1", 1), testEvent("b", "1", 2)
	require.NoError(t, s.Store(ctx, []types.Event{a}))                               // INSERT
	require.NoError(t, s.StoreWindow(ctx, "1", 4, "hash-4", []types.Event{a, a, b})) // COPY, duplicate within batch
	require.NoError(t, s.Store(ctx, []types.Event{b}))                               // older INSERT replay
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM effects").Scan(&count))
	assert.Equal(t, 2, count, "handlers run once per newly inserted ID")
	n, hash, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(4), n, "sparse tail persists; old replay cannot regress cursor")
	assert.Equal(t, "hash-4", hash)
	require.NoError(t, s.StoreWindow(ctx, "1", 6, "hash-6", nil))
	n, _, err = s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(6), n)
}

func TestConcurrentReplayInvokesHandlerOnce(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(map[int]string{1: "COPY", 100: "INSERT"}[threshold], func(t *testing.T) {
			pool := getTestDB(t)
			ctx := context.Background()
			h := handlerFunc(func(ctx context.Context, tx pgx.Tx, e types.Event) error {
				_, err := tx.Exec(ctx, "INSERT INTO effects VALUES ($1)", e.Id)
				return err
			})
			s, err := NewSink(SinkConfig{Pool: pool, Handler: h, CopyThreshold: threshold})
			require.NoError(t, err)
			require.NoError(t, s.Migrate(ctx, "CREATE TABLE effects (id TEXT PRIMARY KEY)"))
			var wg sync.WaitGroup
			errs := make(chan error, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); errs <- s.Store(ctx, []types.Event{testEvent("one", "1", 1)}) }()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			var count int
			require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM effects").Scan(&count))
			assert.Equal(t, 1, count)
		})
	}
}

func TestCommitFailureRollsBackEventsEffectsAndCursor(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()
	h := handlerFunc(func(ctx context.Context, tx pgx.Tx, e types.Event) error {
		_, err := tx.Exec(ctx, "INSERT INTO effects VALUES ($1, 99)", e.Id)
		return err
	})
	s, err := NewSink(SinkConfig{Pool: pool, Handler: h, CopyThreshold: 1})
	require.NoError(t, err)
	require.NoError(t, s.Migrate(ctx, "CREATE TABLE parents (id INT PRIMARY KEY); CREATE TABLE effects (id TEXT PRIMARY KEY, parent INT REFERENCES parents(id) DEFERRABLE INITIALLY DEFERRED)"))
	require.NoError(t, s.UpdateCursor(ctx, "1", 1, "hash-1"))
	err = s.StoreWindow(ctx, "1", 4, "hash-4", []types.Event{testEvent("two", "1", 2)})
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23503", pgErr.Code)
	assert.ErrorContains(t, err, "commit transaction")
	for _, table := range []string{"effects", "chronicle_events"} {
		var count int
		require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count))
		assert.Zero(t, count)
	}
	n, hash, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), n)
	assert.Equal(t, "hash-1", hash)
}

type projectionHandler struct{ rollbackErr error }

func (h *projectionHandler) Handle(ctx context.Context, tx pgx.Tx, e types.Event) error {
	_, err := tx.Exec(ctx, "INSERT INTO projection VALUES ($1, $2, $3)", e.Id, e.ChainId, e.BlockNumber)
	return err
}
func (h *projectionHandler) Rollback(ctx context.Context, tx pgx.Tx, chain string, from uint64) error {
	_, err := tx.Exec(ctx, "DELETE FROM projection WHERE chain_id=$1 AND block_num >= $2", chain, from)
	if err != nil {
		return err
	}
	return h.rollbackErr
}

func TestHandlerRollbackSharesTransaction(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()
	h := &projectionHandler{}
	s, err := NewSink(SinkConfig{Pool: pool, Handler: h})
	require.NoError(t, err)
	require.NoError(t, s.Migrate(ctx, "CREATE TABLE projection (id TEXT PRIMARY KEY, chain_id TEXT, block_num BIGINT)"))
	require.NoError(t, s.Store(ctx, []types.Event{testEvent("a", "1", 1), testEvent("b", "1", 2)}))
	h.rollbackErr = errors.New("rollback failed")
	assert.ErrorIs(t, s.Rollback(ctx, "1", 2, "hash-a"), h.rollbackErr)
	for _, table := range []string{"projection", "chronicle_events"} {
		var count int
		require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count))
		assert.Equal(t, 2, count)
	}
	h.rollbackErr = nil
	require.NoError(t, s.Rollback(ctx, "1", 2, "hash-a"))
	for _, table := range []string{"projection", "chronicle_events"} {
		var count int
		require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count))
		assert.Equal(t, 1, count)
	}
	n, _, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), n)
}

func TestInvalidWindowsRejectedBeforeDatabase(t *testing.T) {
	s := &PGSink{}
	for _, tc := range []struct {
		name   string
		change func(*types.Event)
	}{
		{"different chain", func(e *types.Event) { e.ChainId = "other" }},
		{"missing ID", func(e *types.Event) { e.Id = "" }},
		{"outside window", func(e *types.Event) { e.BlockNumber = 4 }},
		{"hash mismatch", func(e *types.Event) { e.BlockHash = "other" }},
		{"timestamp overflow", func(e *types.Event) { e.Timestamp = math.MaxUint64 }},
		{"index overflow", func(e *types.Event) { e.LogIndex = math.MaxUint64 }},
		{"nil fields", func(e *types.Event) { e.Fields = nil }},
		{"invalid JSON", func(e *types.Event) { e.Fields = types.EventFields{"x": make(chan int)} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := testEvent("a", "1", 2)
			tc.change(&e)
			assert.Error(t, s.StoreWindow(context.Background(), "1", 2, "hash-a", []types.Event{e}))
		})
	}
	assert.Error(t, s.StoreWindow(context.Background(), "1", 4, "hash-4", []types.Event{testEvent("b", "1", 3), testEvent("a", "1", 2)}))
	assert.Error(t, s.StoreWindow(context.Background(), "1", math.MaxUint64, "hash", nil))
}

func TestSchemaInitializationDoesNotDuplicateIndexes(t *testing.T) {
	pool := getTestDB(t)
	count := func() int {
		var n int
		require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND tablename='chronicle_events'").Scan(&n))
		return n
	}
	_, err := NewSink(SinkConfig{Pool: pool, Handler: &mockHandler{}})
	require.NoError(t, err)
	before := count()
	_, err = NewSink(SinkConfig{Pool: pool, Handler: &mockHandler{}})
	require.NoError(t, err)
	assert.Equal(t, before, count())
	assert.Equal(t, 4, before)
}

func TestConflictingWindowCannotReplaceCursorOrEffects(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()
	s, err := NewSink(SinkConfig{Pool: pool, Handler: &mockHandler{}})
	require.NoError(t, err)
	require.NoError(t, s.StoreWindow(ctx, "1", 2, "canonical", nil))
	e := testEvent("orphan", "1", 2)
	assert.ErrorContains(t, s.StoreWindow(ctx, "1", 2, e.BlockHash, []types.Event{e}), "conflicting cursor hash")
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM chronicle_events").Scan(&n))
	assert.Zero(t, n)
	height, hash, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(2), height)
	assert.Equal(t, "canonical", hash)
}

func TestCanceledInitializationAndBeginFailure(t *testing.T) {
	pool := getTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewSinkContext(ctx, SinkConfig{Pool: pool, Handler: &mockHandler{}})
	assert.ErrorIs(t, err, context.Canceled)
	s, err := NewSink(SinkConfig{Pool: pool, Handler: &mockHandler{}})
	require.NoError(t, err)
	pool.Close()
	assert.ErrorContains(t, s.Store(context.Background(), []types.Event{testEvent("a", "1", 1)}), "begin transaction")
}

func TestTypedNilHandlerRejectedBeforeDatabase(t *testing.T) {
	_, err := NewSink(SinkConfig{Pool: &pgxpool.Pool{}, Handler: (*mockHandler)(nil)})
	assert.ErrorContains(t, err, "handler is required")
	_, err = NewSink(SinkConfig{Pool: &pgxpool.Pool{}, Handler: handlerFunc(nil)})
	assert.ErrorContains(t, err, "handler is required")
}

func TestHandlerPanicReleasesTransaction(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()
	s, err := NewSink(SinkConfig{Pool: pool, Handler: handlerFunc(func(context.Context, pgx.Tx, types.Event) error { panic("handler panic") })})
	require.NoError(t, err)
	assert.Panics(t, func() { _ = s.Store(ctx, []types.Event{testEvent("a", "1", 1)}) })
	assert.Zero(t, pool.Stat().AcquiredConns())
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM chronicle_events").Scan(&count))
	assert.Zero(t, count)
}
