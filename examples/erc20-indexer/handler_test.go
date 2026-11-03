package main

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/adapters/sink/postgres"
	"github.com/ryuux05/godex/internal/testutil"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type storeOnly struct{}

func (storeOnly) Handle(context.Context, pgx.Tx, types.Event) error { return nil }

func transfer(id, chain, branch string, block, index uint64, from, to string) types.Event {
	return types.Event{Id: chain + ":" + id, ChainId: chain, EventType: "Transfer", BlockNumber: block, BlockHash: fmt.Sprintf("%s-%d", branch, block), TransactionHash: "same-tx", LogIndex: index, Address: "token", Fields: types.EventFields{"from": from, "to": to, "value": new(big.Int).Lsh(big.NewInt(1), 200)}}
}
func approval(id, branch string, block, index uint64) types.Event {
	e := transfer(id, "1", branch, block, index, "alice", "bob")
	e.EventType = "Approval"
	e.Fields = types.EventFields{"owner": "alice", "spender": "spender", "value": big.NewInt(20)}
	return e
}
func rowCount(t *testing.T, pool *pgxpool.Pool, table, chain string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE chain_id=$1", chain).Scan(&n))
	return n
}
func setup(t *testing.T, threshold int) (*pgxpool.Pool, *postgres.PGSink) {
	t.Helper()
	pool := testutil.Postgres(t)
	h := &ERC20Handler{}
	s, err := postgres.NewSink(postgres.SinkConfig{Pool: pool, Handler: h, CopyThreshold: threshold})
	require.NoError(t, err)
	require.NoError(t, prepareApplication(context.Background(), pool, h, false))
	return pool, s
}

func TestERC20RollbackReplayAndChainIsolation(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			pool, s := setup(t, threshold)
			ctx := context.Background()
			a := transfer("original", "1", "A", 1, 0, "alice", "bob")
			orphan := transfer("orphan", "1", "A", 2, 0, "alice", "orphan-holder")
			require.NoError(t, s.StoreWindow(ctx, "1", 4, "A-4", []types.Event{a, orphan, approval("orphan-approval", "A", 2, 1)}))
			require.NoError(t, s.Store(ctx, []types.Event{transfer("other", "2", "A", 3, 0, "alice", "bob")}))
			require.NoError(t, s.Rollback(ctx, "1", 2, "A-1"))
			assert.Equal(t, 1, rowCount(t, pool, "erc20_transfer_stats", "1"))
			assert.Zero(t, rowCount(t, pool, "erc20_approvals", "1"))
			assert.Equal(t, 2, rowCount(t, pool, "erc20_balances", "1"))
			var height uint64
			require.NoError(t, pool.QueryRow(ctx, "SELECT last_transfer_block FROM erc20_balances WHERE chain_id='1' AND holder_address='alice'").Scan(&height))
			assert.Equal(t, uint64(1), height)
			require.NoError(t, pool.QueryRow(ctx, "SELECT last_transfer_block FROM erc20_balances WHERE chain_id='2' AND holder_address='alice'").Scan(&height))
			assert.Equal(t, uint64(3), height)
			canonical := []types.Event{transfer("replacement", "1", "B", 2, 0, "alice", "replacement-holder"), approval("replacement-approval", "B", 2, 1)}
			require.NoError(t, s.StoreWindow(ctx, "1", 4, "B-4", canonical))
			require.NoError(t, s.StoreWindow(ctx, "1", 4, "B-4", canonical))
			assert.Equal(t, 2, rowCount(t, pool, "erc20_transfer_stats", "1"))
			assert.Equal(t, 1, rowCount(t, pool, "erc20_approvals", "1"))
			var value string
			require.NoError(t, pool.QueryRow(ctx, "SELECT value FROM erc20_transfer_stats WHERE event_id='1:replacement'").Scan(&value))
			assert.Equal(t, new(big.Int).Lsh(big.NewInt(1), 200).String(), value)
			n, hash, err := s.LoadCursor(ctx, "1")
			require.NoError(t, err)
			assert.Equal(t, uint64(4), n)
			assert.Equal(t, "B-4", hash)
			require.NoError(t, s.Rollback(ctx, "1", 0, ""))
			assert.Zero(t, rowCount(t, pool, "erc20_transfer_stats", "1"))
			assert.Zero(t, rowCount(t, pool, "erc20_balances", "1"))
			assert.Equal(t, 1, rowCount(t, pool, "erc20_transfer_stats", "2"))
		})
	}
}

func TestERC20FailedRollbackRestoresProjections(t *testing.T) {
	pool, s := setup(t, 1)
	ctx := context.Background()
	require.NoError(t, s.Store(ctx, []types.Event{transfer("a", "1", "A", 1, 0, "alice", "bob"), approval("b", "A", 2, 0)}))
	_, err := pool.Exec(ctx, "ALTER TABLE chronicle_cursors ADD CHECK(block_num>0)")
	require.NoError(t, err)
	assert.Error(t, s.Rollback(ctx, "1", 1, ""))
	assert.Equal(t, 1, rowCount(t, pool, "erc20_transfer_stats", "1"))
	assert.Equal(t, 1, rowCount(t, pool, "erc20_approvals", "1"))
	assert.Equal(t, 2, rowCount(t, pool, "erc20_balances", "1"))
	assert.Equal(t, 2, rowCount(t, pool, "chronicle_events", "1"))
	n, _, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(2), n)
}

func TestERC20HandlerFailureRollsBackEarlierEffects(t *testing.T) {
	pool, s := setup(t, 1)
	ctx := context.Background()
	a := transfer("good", "1", "A", 1, 0, "alice", "bob")
	bad := approval("bad", "A", 2, 0)
	bad.Fields["value"] = "wrong-type"
	assert.ErrorContains(t, s.Store(ctx, []types.Event{a, bad}), "invalid 'value'")
	assert.Zero(t, rowCount(t, pool, "erc20_transfer_stats", "1"))
	assert.Zero(t, rowCount(t, pool, "erc20_balances", "1"))
	assert.Zero(t, rowCount(t, pool, "chronicle_events", "1"))
}

func TestERC20LegacyRebuildPreservesCursorAndIntegerPrecision(t *testing.T) {
	pool := testutil.Postgres(t)
	ctx := context.Background()
	s, err := postgres.NewSink(postgres.SinkConfig{Pool: pool, Handler: storeOnly{}})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "CREATE TABLE erc20_transfer_stats(id INT PRIMARY KEY); INSERT INTO erc20_transfer_stats VALUES(999)")
	require.NoError(t, err)
	var events []types.Event
	for n := uint64(1); n <= 257; n++ {
		events = append(events, transfer(fmt.Sprint(n), "1", "A", n, 0, "alice", "bob"))
	}
	require.NoError(t, s.StoreWindow(ctx, "1", 300, "A-300", events))
	h := &ERC20Handler{}
	assert.ErrorContains(t, prepareApplication(ctx, pool, h, false), "REBUILD_PROJECTIONS=1")
	var legacyID int
	require.NoError(t, pool.QueryRow(ctx, "SELECT id FROM erc20_transfer_stats").Scan(&legacyID))
	assert.Equal(t, 999, legacyID)
	require.NoError(t, prepareApplication(ctx, pool, h, true))
	assert.Equal(t, 257, rowCount(t, pool, "erc20_transfer_stats", "1"))
	var value string
	require.NoError(t, pool.QueryRow(ctx, "SELECT value FROM erc20_transfer_stats LIMIT 1").Scan(&value))
	assert.Equal(t, new(big.Int).Lsh(big.NewInt(1), 200).String(), value)
	n, hash, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(300), n)
	assert.Equal(t, "A-300", hash)
	require.NoError(t, prepareApplication(ctx, pool, h, false))
	require.NoError(t, prepareApplication(ctx, pool, h, true))
	assert.Equal(t, 257, rowCount(t, pool, "erc20_transfer_stats", "1"), "rebuild must be repeatable")
}

func TestERC20FailedLegacyRebuildIsAtomic(t *testing.T) {
	pool := testutil.Postgres(t)
	ctx := context.Background()
	s, err := postgres.NewSink(postgres.SinkConfig{Pool: pool, Handler: storeOnly{}})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "CREATE TABLE erc20_transfer_stats(id INT PRIMARY KEY); INSERT INTO erc20_transfer_stats VALUES(999)")
	require.NoError(t, err)
	bad := transfer("bad", "1", "A", 2, 0, "alice", "bob")
	bad.Fields["value"] = "invalid"
	require.NoError(t, s.Store(ctx, []types.Event{transfer("a", "1", "A", 1, 0, "alice", "bob"), bad}))
	assert.Error(t, prepareApplication(ctx, pool, &ERC20Handler{}, true))
	var legacyID int
	require.NoError(t, pool.QueryRow(ctx, "SELECT id FROM erc20_transfer_stats").Scan(&legacyID))
	assert.Equal(t, 999, legacyID)
	assert.Equal(t, 2, rowCount(t, pool, "chronicle_events", "1"))
	n, _, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(2), n)
}
