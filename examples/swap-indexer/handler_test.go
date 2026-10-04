package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
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
func handler() *UniswapHandler {
	return &UniswapHandler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}
func baseEvent(id, chain, branch string, n, index uint64) types.Event {
	return types.Event{Id: chain + ":" + id, ChainId: chain, BlockNumber: n, BlockHash: fmt.Sprintf("%s-%d", branch, n), LogIndex: index, TransactionHash: "same-tx", Address: "manager", Timestamp: 100, Fields: types.EventFields{}}
}
func initialization(id, chain, branch string, n uint64, poolByte byte, token0 string) types.Event {
	e := baseEvent(id, chain, branch, n, 0)
	e.EventType = "Initialize"
	e.Fields = types.EventFields{"id": bytes.Repeat([]byte{poolByte}, 32), "currency0": token0, "currency1": "token1", "fee": big.NewInt(500), "tickSpacing": big.NewInt(10), "hooks": "hooks", "sqrtPriceX96": new(big.Int).Lsh(big.NewInt(1), 96), "tick": big.NewInt(-10)}
	return e
}
func swap(id, chain, branch string, n, index uint64, a0, a1 int64) types.Event {
	e := baseEvent(id, chain, branch, n, index)
	e.EventType = "Swap"
	e.Fields = types.EventFields{"id": bytes.Repeat([]byte{42}, 32), "sender": "alice", "amount0": big.NewInt(a0), "amount1": big.NewInt(a1), "sqrtPriceX96": new(big.Int).Lsh(big.NewInt(1), 96), "liquidity": big.NewInt(100), "tick": big.NewInt(-10), "fee": big.NewInt(500)}
	return e
}
func setup(t *testing.T, threshold int) (*pgxpool.Pool, *postgres.PGSink) {
	t.Helper()
	pool := testutil.Postgres(t)
	h := handler()
	s, err := postgres.NewSink(postgres.SinkConfig{Pool: pool, Handler: h, CopyThreshold: threshold})
	require.NoError(t, err)
	require.NoError(t, prepareApplication(context.Background(), pool, h, false))
	return pool, s
}
func rowCount(t *testing.T, pool *pgxpool.Pool, table, chain string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE chain_id=$1", chain).Scan(&n))
	return n
}
func connectionCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM swap_connections").Scan(&n))
	return n
}
func stats(t *testing.T, pool *pgxpool.Pool, chain string) (uint64, string, string) {
	t.Helper()
	var count uint64
	var a, b string
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT swap_count,total_volume0::text,total_volume1::text FROM uniswap_pool_stats WHERE chain_id=$1 AND contract_address='manager'", chain).Scan(&count, &a, &b))
	return count, a, b
}

func TestSwapRollbackRestoresHistoryPoolsStatisticsAndConnections(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			pool, s := setup(t, threshold)
			ctx := context.Background()
			original := []types.Event{initialization("init", "1", "A", 1, 42, "original-token"), swap("survivor", "1", "A", 1, 1, 8, -9), swap("a", "1", "A", 2, 0, 10, -20), swap("b", "1", "A", 2, 1, -30, 40), initialization("update", "1", "A", 3, 42, "orphan-token"), initialization("orphan-pool", "1", "A", 4, 43, "orphan-token")}
			require.NoError(t, s.Store(ctx, original))
			assert.Equal(t, 3, rowCount(t, pool, "uniswap_swaps", "1"), "distinct logs in one transaction must both persist")
			count, a, b := stats(t, pool, "1")
			assert.Equal(t, uint64(3), count)
			assert.Equal(t, "48", a)
			assert.Equal(t, "69", b)
			require.NoError(t, s.Store(ctx, []types.Event{initialization("init", "2", "A", 1, 42, "other-token"), swap("swap", "2", "A", 2, 0, 5, -6)}))
			assert.Equal(t, 3, connectionCount(t, pool), "cross-chain connection queries must not reuse a busy connection or underflow timestamps")
			require.NoError(t, s.Rollback(ctx, "1", 2, "A-1"))
			assert.Equal(t, 1, rowCount(t, pool, "uniswap_swaps", "1"))
			count, a, b = stats(t, pool, "1")
			assert.Equal(t, uint64(1), count)
			assert.Equal(t, "8", a)
			assert.Equal(t, "9", b)
			assert.Equal(t, 1, connectionCount(t, pool), "connections between surviving swaps must remain")
			assert.Equal(t, 1, rowCount(t, pool, "uniswap_pool_initializations", "1"))
			assert.Equal(t, 1, rowCount(t, pool, "uniswap_pools", "1"))
			var token string
			var first, last uint64
			require.NoError(t, pool.QueryRow(ctx, "SELECT token0_address,first_seen_block,last_seen_block FROM uniswap_pools WHERE chain_id='1'").Scan(&token, &first, &last))
			assert.Equal(t, "original-token", token)
			assert.Equal(t, uint64(1), first)
			assert.Equal(t, uint64(1), last)
			require.NoError(t, pool.QueryRow(ctx, "SELECT token0_address FROM uniswap_pools WHERE chain_id='2'").Scan(&token))
			assert.Equal(t, "other-token", token)
			count, a, b = stats(t, pool, "2")
			assert.Equal(t, uint64(1), count)
			assert.Equal(t, "5", a)
			assert.Equal(t, "6", b)
			canonical := []types.Event{swap("replacement-a", "1", "B", 2, 0, 3, -7), swap("replacement-b", "1", "B", 2, 1, 4, -8), initialization("replacement-pool", "1", "B", 3, 43, "replacement-token")}
			require.NoError(t, s.StoreWindow(ctx, "1", 4, "B-4", canonical))
			require.NoError(t, s.StoreWindow(ctx, "1", 4, "B-4", canonical))
			count, a, b = stats(t, pool, "1")
			assert.Equal(t, uint64(3), count)
			assert.Equal(t, "15", a)
			assert.Equal(t, "24", b)
			assert.Equal(t, 2, rowCount(t, pool, "uniswap_pools", "1"))
			assert.Equal(t, 5, rowCount(t, pool, "chronicle_events", "1"))
			n, hash, err := s.LoadCursor(ctx, "1")
			require.NoError(t, err)
			assert.Equal(t, uint64(4), n)
			assert.Equal(t, "B-4", hash)
			require.NoError(t, s.Rollback(ctx, "1", 0, ""))
			assert.Zero(t, rowCount(t, pool, "uniswap_pools", "1"))
			assert.Zero(t, rowCount(t, pool, "uniswap_pool_stats", "1"))
			assert.Zero(t, connectionCount(t, pool))
			assert.Equal(t, 1, rowCount(t, pool, "uniswap_swaps", "2"))
		})
	}
}

func TestSwapFailedRollbackRestoresAllProjections(t *testing.T) {
	pool, s := setup(t, 1)
	ctx := context.Background()
	require.NoError(t, s.Store(ctx, []types.Event{initialization("init", "1", "A", 1, 42, "token"), swap("swap", "1", "A", 2, 0, 10, -20)}))
	_, err := pool.Exec(ctx, "ALTER TABLE chronicle_cursors ADD CHECK(block_num>0)")
	require.NoError(t, err)
	assert.Error(t, s.Rollback(ctx, "1", 1, ""))
	assert.Equal(t, 1, rowCount(t, pool, "uniswap_pools", "1"))
	assert.Equal(t, 1, rowCount(t, pool, "uniswap_swaps", "1"))
	assert.Equal(t, 1, rowCount(t, pool, "uniswap_pool_initializations", "1"))
	count, a, b := stats(t, pool, "1")
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, "10", a)
	assert.Equal(t, "20", b)
	n, _, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(2), n)
}

func TestSwapHandlerFailureRollsBackEarlierEffects(t *testing.T) {
	pool, s := setup(t, 1)
	ctx := context.Background()
	bad := swap("bad", "1", "A", 2, 0, 10, 20)
	bad.Fields["sender"] = 42
	assert.ErrorContains(t, s.Store(ctx, []types.Event{initialization("init", "1", "A", 1, 42, "token"), bad}), "invalid 'sender'")
	assert.Zero(t, rowCount(t, pool, "uniswap_pools", "1"))
	assert.Zero(t, rowCount(t, pool, "uniswap_pool_initializations", "1"))
	assert.Zero(t, rowCount(t, pool, "chronicle_events", "1"))
}

func TestSwapConnectionFailureRollsBackWindow(t *testing.T) {
	pool, s := setup(t, 1)
	ctx := context.Background()
	require.NoError(t, s.Store(ctx, []types.Event{initialization("init", "2", "A", 1, 42, "token"), swap("other", "2", "A", 2, 0, 1, -2)}))
	_, err := pool.Exec(ctx, "ALTER TABLE swap_connections ADD CHECK(false)")
	require.NoError(t, err)
	assert.ErrorContains(t, s.Store(ctx, []types.Event{swap("new", "1", "A", 2, 0, 3, -4)}), "connect swaps")
	assert.Zero(t, rowCount(t, pool, "uniswap_swaps", "1"))
	assert.Zero(t, rowCount(t, pool, "uniswap_pool_stats", "1"))
	assert.Zero(t, rowCount(t, pool, "chronicle_events", "1"))
	assert.Equal(t, 1, rowCount(t, pool, "uniswap_swaps", "2"))
}

func TestSwapLegacyRebuildRecoversDistinctLogsAndBytes(t *testing.T) {
	pool := testutil.Postgres(t)
	ctx := context.Background()
	s, err := postgres.NewSink(postgres.SinkConfig{Pool: pool, Handler: storeOnly{}})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "CREATE TABLE uniswap_swaps(id INT PRIMARY KEY); INSERT INTO uniswap_swaps VALUES(999)")
	require.NoError(t, err)
	init := initialization("init", "1", "A", 1, 42, "token")
	a := swap("a", "1", "A", 2, 0, 10, -20)
	b := swap("b", "1", "A", 2, 1, -30, 40)
	a.Fields["amount0"] = new(big.Int).Lsh(big.NewInt(1), 120)
	require.NoError(t, s.StoreWindow(ctx, "1", 4, "A-4", []types.Event{init, a, b}))
	assert.ErrorContains(t, prepareApplication(ctx, pool, handler(), false), "REBUILD_PROJECTIONS=1")
	require.NoError(t, prepareApplication(ctx, pool, handler(), true))
	assert.Equal(t, 2, rowCount(t, pool, "uniswap_swaps", "1"))
	assert.Equal(t, 1, rowCount(t, pool, "uniswap_pool_initializations", "1"))
	var id string
	require.NoError(t, pool.QueryRow(ctx, "SELECT pool_id FROM uniswap_pools WHERE chain_id='1'").Scan(&id))
	assert.Equal(t, fmt.Sprintf("0x%x", bytes.Repeat([]byte{42}, 32)), id)
	count, v0, v1 := stats(t, pool, "1")
	assert.Equal(t, uint64(2), count)
	assert.Equal(t, new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 120), big.NewInt(30)).String(), v0)
	assert.Equal(t, "60", v1)
	n, hash, err := s.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(4), n)
	assert.Equal(t, "A-4", hash)
	require.NoError(t, prepareApplication(ctx, pool, handler(), false))
	require.NoError(t, prepareApplication(ctx, pool, handler(), true))
	assert.Equal(t, 2, rowCount(t, pool, "uniswap_swaps", "1"))
}
