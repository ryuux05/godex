package postgres

import (
	"context"
	"os"
	"testing"
	"time"
	"strconv"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getTestDB creates a test database connection
// Set POSTGRES_TEST_DSN environment variable or use default
func getTestDB(t *testing.T) *pgxpool.Pool {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:dev@localhost:5432/postgres?sslmode=disable"
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("Skipping test: unable to connect to test database: %v", err)
		return nil
	}

	// Test connection
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("Skipping test: unable to ping test database: %v", err)
		return nil
	}

	return pool
}

// cleanupTestDB cleans up test data
func cleanupTestDB(t *testing.T, pool *pgxpool.Pool) {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		TRUNCATE TABLE chronicle_events CASCADE;
		TRUNCATE TABLE chronicle_cursors CASCADE;
	`)
	if err != nil {
		t.Logf("Warning: failed to cleanup test database: %v", err)
	}
}

// mockHandler is a test handler implementation
type mockHandler struct {
	events []types.Event
	errors map[int]error // event index -> error
}

func (m *mockHandler) Handle(ctx context.Context, tx pgx.Tx, ev types.Event) error {
	if m.errors != nil {
		if err, ok := m.errors[len(m.events)]; ok {
			return err
		}
	}
	m.events = append(m.events, ev)
	return nil
}

func TestNewSink(t *testing.T) {
	pool := getTestDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	t.Run("success", func(t *testing.T) {
		handler := &mockHandler{}
		sink, err := NewSink(SinkConfig{
			Pool:          pool,
			Handler:       handler,
			CopyThreshold: 10,
		})
		require.NoError(t, err)
		assert.NotNil(t, sink)
	})

	t.Run("missing pool", func(t *testing.T) {
		handler := &mockHandler{}
		_, err := NewSink(SinkConfig{
			Pool:    nil,
			Handler: handler,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Pool is required")
	})

	t.Run("missing handler", func(t *testing.T) {
		_, err := NewSink(SinkConfig{
			Pool:    pool,
			Handler: nil,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Handler is required")
	})

	t.Run("default copy threshold", func(t *testing.T) {
		handler := &mockHandler{}
		sink, err := NewSink(SinkConfig{
			Pool:    pool,
			Handler: handler,
		})
		require.NoError(t, err)
		assert.Equal(t, 32, sink.copyThreshold) // or 64, depending on your default
	})
}

func TestStore_InsertMode(t *testing.T) {
	pool := getTestDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	defer cleanupTestDB(t, pool)

	handler := &mockHandler{}
	sink, err := NewSink(SinkConfig{
		Pool:          pool,
		Handler:       handler,
		CopyThreshold: 100, // Force insert mode
	})
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("empty events", func(t *testing.T) {
		err := sink.Store(ctx, []types.Event{})
		assert.NoError(t, err)
	})

	t.Run("single event", func(t *testing.T) {
		events := []types.Event{
			{
				Id:              "test-1",
				ChainId:         "1",
				EventType:       "Transfer",
				BlockNumber:     100,
				BlockHash:       "0xabc",
				TransactionHash: "0xdef",
				LogIndex:        0,
				Address:         "0x123",
				Timestamp:       uint64(time.Now().Unix()),
				Fields:          types.EventFields{"from": "0xa", "to": "0xb", "value": "100"},
			},
		}

		err := sink.Store(ctx, events)
		assert.NoError(t, err)

		// Verify event stored
		var count int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM chronicle_events WHERE event_id = $1", "test-1").Scan(&count)
		assert.NoError(t, err)
		assert.Equal(t, 1, count)

		// Verify handler called
		assert.Len(t, handler.events, 1)
		assert.Equal(t, "test-1", handler.events[0].Id)

		// Verify cursor updated
		var blockNum uint64
		err = pool.QueryRow(ctx, "SELECT block_num FROM chronicle_cursors WHERE chain_id = $1", "1").Scan(&blockNum)
		assert.NoError(t, err)
		assert.Equal(t, uint64(100), blockNum)
	})

	t.Run("multiple events", func(t *testing.T) {
		cleanupTestDB(t, pool)
		handler.events = nil

		events := []types.Event{
			{
				Id:              "test-2",
				ChainId:         "1",
				EventType:       "Transfer",
				BlockNumber:     101,
				BlockHash:       "0xabc1",
				TransactionHash: "0xdef1",
				LogIndex:        0,
				Address:         "0x123",
				Timestamp:       uint64(time.Now().Unix()),
				Fields:          types.EventFields{"from": "0xa", "to": "0xb"},
			},
			{
				Id:              "test-3",
				ChainId:         "1",
				EventType:       "Approval",
				BlockNumber:     102,
				BlockHash:       "0xabc2",
				TransactionHash: "0xdef2",
				LogIndex:        0,
				Address:         "0x456",
				Timestamp:       uint64(time.Now().Unix()),
				Fields:          types.EventFields{"owner": "0xc", "spender": "0xd"},
			},
		}

		err := sink.Store(ctx, events)
		assert.NoError(t, err)

		// Verify both events stored
		var count int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM chronicle_events WHERE event_id IN ($1, $2)", "test-2", "test-3").Scan(&count)
		assert.NoError(t, err)
		assert.Equal(t, 2, count)

		// Verify handler called for both
		assert.Len(t, handler.events, 2)

		// Verify cursor updated to last block
		var blockNum uint64
		err = pool.QueryRow(ctx, "SELECT block_num FROM chronicle_cursors WHERE chain_id = $1", "1").Scan(&blockNum)
		assert.NoError(t, err)
		assert.Equal(t, uint64(102), blockNum)
	})
}

func TestStore_CopyMode(t *testing.T) {
	pool := getTestDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	defer cleanupTestDB(t, pool)

	handler := &mockHandler{}
	sink, err := NewSink(SinkConfig{
		Pool:          pool,
		Handler:       handler,
		CopyThreshold: 2, // Force copy mode
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Create events above threshold
	events := make([]types.Event, 5)
	for i := 0; i < 5; i++ {
		events[i] = types.Event{
			Id:              "copy-test-" + strconv.Itoa(i),
			ChainId:         "1",
			EventType:       "Transfer",
			BlockNumber:     uint64(200 + i),
			BlockHash:       "0xhash" + strconv.Itoa(i),
			TransactionHash: "0xtx" + strconv.Itoa(i),
			LogIndex:        uint64(i),
			Address:         "0xaddr",
			Timestamp:       uint64(time.Now().Unix()),
			Fields:          types.EventFields{"value": i},
		}
	}

	err = sink.Store(ctx, events)
	assert.NoError(t, err)

	// Verify all events stored
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM chronicle_events WHERE chain_id = $1 AND block_num >= 200", "1").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 5, count)

	// Verify handler called for all
	assert.Len(t, handler.events, 5)
}

func TestStore_HandlerFailure(t *testing.T) {
	pool := getTestDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	defer cleanupTestDB(t, pool)

	handler := &mockHandler{
		errors: map[int]error{
			1: assert.AnError, // Fail on second event
		},
	}
	sink, err := NewSink(SinkConfig{
		Pool:          pool,
		Handler:       handler,
		CopyThreshold: 100,
	})
	require.NoError(t, err)

	ctx := context.Background()

	events := []types.Event{
		{Id: "test-fail-1", ChainId: "1", EventType: "Transfer", BlockNumber: 300, BlockHash: "0x1", TransactionHash: "0xtx1", LogIndex: 0, Address: "0xaddr", Timestamp: uint64(time.Now().Unix()), Fields: types.EventFields{}},
		{Id: "test-fail-2", ChainId: "1", EventType: "Transfer", BlockNumber: 301, BlockHash: "0x2", TransactionHash: "0xtx2", LogIndex: 1, Address: "0xaddr", Timestamp: uint64(time.Now().Unix()), Fields: types.EventFields{}},
	}

	err = sink.Store(ctx, events)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "handler failed")

	// Verify NO events stored (atomicity)
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM chronicle_events WHERE event_id IN ($1, $2)", "test-fail-1", "test-fail-2").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 0, count) // Should be 0 due to rollback
}

func TestRollback(t *testing.T) {
	pool := getTestDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	defer cleanupTestDB(t, pool)

	handler := &mockHandler{}
	sink, err := NewSink(SinkConfig{
		Pool:          pool,
		Handler:       handler,
		CopyThreshold: 100,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Store some events first
	events := []types.Event{
		{Id: "rollback-1", ChainId: "1", EventType: "Transfer", BlockNumber: 400, BlockHash: "0x1", TransactionHash: "0xtx1", LogIndex: 0, Address: "0xaddr", Timestamp: uint64(time.Now().Unix()), Fields: types.EventFields{}},
		{Id: "rollback-2", ChainId: "1", EventType: "Transfer", BlockNumber: 401, BlockHash: "0x2", TransactionHash: "0xtx2", LogIndex: 1, Address: "0xaddr", Timestamp: uint64(time.Now().Unix()), Fields: types.EventFields{}},
		{Id: "rollback-3", ChainId: "1", EventType: "Transfer", BlockNumber: 402, BlockHash: "0x3", TransactionHash: "0xtx3", LogIndex: 2, Address: "0xaddr", Timestamp: uint64(time.Now().Unix()), Fields: types.EventFields{}},
	}

	err = sink.Store(ctx, events)
	require.NoError(t, err)

	// Verify events stored
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM chronicle_events WHERE chain_id = $1", "1").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 3, count)

	// Rollback to block 401 (should delete block 401 and 402)
	err = sink.Rollback(ctx, "1", 401)
	assert.NoError(t, err)

	// Verify only block 400 remains
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM chronicle_events WHERE chain_id = $1 AND block_num < 401", "1").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify events >= 401 deleted
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM chronicle_events WHERE chain_id = $1 AND block_num >= 401", "1").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 0, count)

	// Verify cursor updated
	var blockNum uint64
	err = pool.QueryRow(ctx, "SELECT block_num FROM chronicle_cursors WHERE chain_id = $1", "1").Scan(&blockNum)
	assert.NoError(t, err)
	assert.Equal(t, uint64(400), blockNum)
}

func TestRollback_ToZero(t *testing.T) {
	pool := getTestDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	defer cleanupTestDB(t, pool)

	handler := &mockHandler{}
	sink, err := NewSink(SinkConfig{
		Pool:          pool,
		Handler:       handler,
		CopyThreshold: 100,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Store event at block 1
	events := []types.Event{
		{Id: "rollback-zero", ChainId: "1", EventType: "Transfer", BlockNumber: 1, BlockHash: "0x1", TransactionHash: "0xtx1", LogIndex: 0, Address: "0xaddr", Timestamp: uint64(time.Now().Unix()), Fields: types.EventFields{}},
	}
	err = sink.Store(ctx, events)
	require.NoError(t, err)

	// Rollback to block 0
	err = sink.Rollback(ctx, "1", 0)
	assert.NoError(t, err)

	// Verify cursor is 0
	var blockNum uint64
	err = pool.QueryRow(ctx, "SELECT block_num FROM chronicle_cursors WHERE chain_id = $1", "1").Scan(&blockNum)
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), blockNum)
}

func TestMigrate(t *testing.T) {
	pool := getTestDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	handler := &mockHandler{}
	sink, err := NewSink(SinkConfig{
		Pool:    pool,
		Handler: handler,
	})
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		sql := `
			CREATE TABLE IF NOT EXISTS test_migration (
				id SERIAL PRIMARY KEY,
				name TEXT NOT NULL
			);
		`
		err := sink.Migrate(ctx, sql)
		assert.NoError(t, err)

		// Verify table created
		var exists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT FROM information_schema.tables 
				WHERE table_name = 'test_migration'
			)
		`).Scan(&exists)
		assert.NoError(t, err)
		assert.True(t, exists)

		// Cleanup
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS test_migration")
	})

	t.Run("empty sql", func(t *testing.T) {
		err := sink.Migrate(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "sql string cannot be empty")
	})

	t.Run("invalid sql", func(t *testing.T) {
		err := sink.Migrate(ctx, "INVALID SQL SYNTAX!!!")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "migration execution failed")
	})
}

func TestMigrateWithFile(t *testing.T) {
	pool := getTestDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	handler := &mockHandler{}
	sink, err := NewSink(SinkConfig{
		Pool:    pool,
		Handler: handler,
	})
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		// Create temporary SQL file
		tmpFile, err := os.CreateTemp("", "test_migration_*.sql")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		sql := `
			CREATE TABLE IF NOT EXISTS test_file_migration (
				id SERIAL PRIMARY KEY,
				value TEXT
			);
		`
		_, err = tmpFile.WriteString(sql)
		require.NoError(t, err)
		tmpFile.Close()

		err = sink.MigrateWithFile(ctx, tmpFile.Name())
		assert.NoError(t, err)

		// Verify table created
		var exists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT FROM information_schema.tables 
				WHERE table_name = 'test_file_migration'
			)
		`).Scan(&exists)
		assert.NoError(t, err)
		assert.True(t, exists)

		// Cleanup
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS test_file_migration")
	})

	t.Run("empty file path", func(t *testing.T) {
		err := sink.MigrateWithFile(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "file path cannot be empty")
	})

	t.Run("file not found", func(t *testing.T) {
		err := sink.MigrateWithFile(ctx, "/nonexistent/file.sql")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read schema file")
	})
}
