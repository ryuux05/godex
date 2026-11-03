package main

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/adapters/sink/postgres"
	"github.com/ryuux05/godex/internal/testutil"
	"github.com/ryuux05/godex/pkg/core/decoder"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
	"github.com/ryuux05/godex/pkg/godex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tokenA = "0x1111111111111111111111111111111111111111"
	tokenB = "0x2222222222222222222222222222222222222222"
	alice  = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bob    = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	carol  = "0xcccccccccccccccccccccccccccccccccccccccc"
	zero   = string(types.ZeroAddress)
)

func addressTopic(address string) string {
	return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(address, "0x")
}

func changeLog(block, index uint64, delegator, from, to string) godex.Log {
	return godex.Log{
		Address: tokenA,
		Topics: []string{utils.FunctionSignatureToTopic("DelegateChanged(address,address,address)"),
			addressTopic(delegator), addressTopic(from), addressTopic(to)},
		Data: "0x", BlockNumber: fmt.Sprintf("0x%x", block), BlockHash: fmt.Sprintf("A-%d", block),
		TransactionHash: fmt.Sprintf("tx-%d", block), LogIndex: fmt.Sprintf("0x%x", index),
	}
}

func votesLog(block, index uint64, delegate string, previous, votes *big.Int) godex.Log {
	l := changeLog(block, index, alice, zero, delegate)
	l.Topics = []string{utils.FunctionSignatureToTopic("DelegateVotesChanged(address,uint256,uint256)"), addressTopic(delegate)}
	l.Data = fmt.Sprintf("0x%064x%064x", previous, votes)
	return l
}

func decodeLogs(t *testing.T, chain string, logs ...godex.Log) []godex.Event {
	t.Helper()
	d := decoder.NewStandardDecoder()
	require.NoError(t, d.RegisterABI("votes", votesABI))
	events := make([]godex.Event, 0, len(logs))
	for _, l := range logs {
		l.TransactionHash += "-chain-" + chain
		event, err := d.Decode("votes", chain, l)
		require.NoError(t, err)
		require.NotNil(t, event)
		events = append(events, *event)
	}
	return events
}

func setup(t *testing.T, threshold int) (*pgxpool.Pool, *postgres.PGSink) {
	t.Helper()
	pool := testutil.Postgres(t)
	sink, err := postgres.NewSink(postgres.SinkConfig{Pool: pool, Handler: DelegationHandler{}, CopyThreshold: threshold})
	require.NoError(t, err)
	require.NoError(t, prepareApplication(context.Background(), pool))
	return pool, sink
}

func assertDelegate(t *testing.T, pool *pgxpool.Pool, chain, token, delegator, want string) {
	t.Helper()
	var got string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT delegate FROM delegation_current
		WHERE chain_id=$1 AND contract_address=$2 AND delegator=$3`, chain, token, delegator).Scan(&got))
	assert.Equal(t, want, got)
}

func assertVotes(t *testing.T, pool *pgxpool.Pool, chain, token, delegate, want string) {
	t.Helper()
	var got string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT votes::text FROM delegation_voting_power
		WHERE chain_id=$1 AND contract_address=$2 AND delegate=$3`, chain, token, delegate).Scan(&got))
	assert.Equal(t, want, got)
}

func countRows(t *testing.T, pool *pgxpool.Pool, table, chain string) int {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE chain_id=$1", chain).Scan(&count))
	return count
}

func TestDelegationLifecycleAndReplay(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			pool, sink := setup(t, threshold)
			ctx := context.Background()
			large := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(10))
			increased := new(big.Int).Add(large, big.NewInt(9))
			store := func(block uint64, logs ...godex.Log) {
				events := decodeLogs(t, "1", logs...)
				for i := 0; i < 2; i++ {
					require.NoError(t, sink.StoreWindow(ctx, "1", block, fmt.Sprintf("A-%d", block), events))
				}
			}
			store(1, changeLog(1, 0, alice, zero, bob), votesLog(1, 1, bob, big.NewInt(0), large))
			assertDelegate(t, pool, "1", tokenA, alice, bob)
			assertVotes(t, pool, "1", tokenA, bob, large.String())

			// Token transfers change voting power without changing the delegate.
			store(2, votesLog(2, 0, bob, large, increased))
			assertDelegate(t, pool, "1", tokenA, alice, bob)
			assertVotes(t, pool, "1", tokenA, bob, increased.String())

			store(3, changeLog(3, 0, alice, bob, carol), votesLog(3, 1, bob, increased, big.NewInt(0)),
				votesLog(3, 2, carol, big.NewInt(0), increased))
			assertDelegate(t, pool, "1", tokenA, alice, carol)
			assertVotes(t, pool, "1", tokenA, bob, "0")
			assertVotes(t, pool, "1", tokenA, carol, increased.String())

			store(4, changeLog(4, 0, alice, carol, zero), votesLog(4, 1, carol, increased, big.NewInt(0)))
			assertDelegate(t, pool, "1", tokenA, alice, zero)
			assertVotes(t, pool, "1", tokenA, carol, "0")
			var active int
			require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM delegation_current WHERE delegate <> $1", zero).Scan(&active))
			assert.Zero(t, active)

			store(5, changeLog(5, 0, alice, zero, alice), votesLog(5, 1, alice, big.NewInt(0), big.NewInt(7)))
			assertDelegate(t, pool, "1", tokenA, alice, alice)
			assertVotes(t, pool, "1", tokenA, alice, "7")
			assert.Equal(t, 4, countRows(t, pool, "delegation_changes", "1"))
			assert.Equal(t, 6, countRows(t, pool, "delegation_vote_changes", "1"))
			assert.Equal(t, 10, countRows(t, pool, "chronicle_events", "1"))
		})
	}
}

func TestDelegationSameBlockOrderingRollbackAndRestart(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			pool, sink := setup(t, threshold)
			ctx := context.Background()
			otherToken := changeLog(1, 2, alice, zero, carol)
			otherToken.Address = tokenB
			otherVotes := votesLog(1, 3, carol, big.NewInt(0), big.NewInt(31))
			otherVotes.Address = tokenB
			require.NoError(t, sink.Store(ctx, decodeLogs(t, "1", changeLog(1, 0, alice, zero, bob),
				votesLog(1, 1, bob, big.NewInt(0), big.NewInt(17)), otherToken, otherVotes)))
			otherChain := decodeLogs(t, "2", changeLog(2, 0, alice, zero, alice), votesLog(2, 1, alice, big.NewInt(0), big.NewInt(9)))
			require.NoError(t, sink.Store(ctx, otherChain))

			orphaned := decodeLogs(t, "1", changeLog(2, 0, alice, bob, carol),
				votesLog(2, 1, bob, big.NewInt(17), big.NewInt(0)), votesLog(2, 2, carol, big.NewInt(0), big.NewInt(17)),
				changeLog(2, 3, alice, carol, zero), votesLog(2, 4, carol, big.NewInt(17), big.NewInt(0)))
			require.NoError(t, sink.StoreWindow(ctx, "1", 4, "A-4", orphaned))
			assertDelegate(t, pool, "1", tokenA, alice, zero)
			assertVotes(t, pool, "1", tokenA, carol, "0")
			assert.Equal(t, 4, countRows(t, pool, "delegation_changes", "1"), "separate logs in one transaction must survive")

			require.NoError(t, sink.Rollback(ctx, "1", 2, "A-1"))
			assertDelegate(t, pool, "1", tokenA, alice, bob)
			assertVotes(t, pool, "1", tokenA, bob, "17")
			assertDelegate(t, pool, "1", tokenB, alice, carol)
			assertVotes(t, pool, "1", tokenB, carol, "31")
			assertDelegate(t, pool, "2", tokenA, alice, alice)
			assertVotes(t, pool, "2", tokenA, alice, "9")
			assert.Equal(t, 2, countRows(t, pool, "delegation_changes", "1"))
			assert.Equal(t, 2, countRows(t, pool, "delegation_vote_changes", "1"))
			assert.Equal(t, 2, countRows(t, pool, "delegation_voting_power", "1"), "orphan-only delegates disappear")
			height, hash, err := sink.LoadCursor(ctx, "1")
			require.NoError(t, err)
			assert.Equal(t, uint64(1), height)
			assert.Equal(t, "A-1", hash)

			replacement := []godex.Log{changeLog(2, 0, alice, bob, carol),
				votesLog(2, 1, bob, big.NewInt(17), big.NewInt(0)), votesLog(2, 2, carol, big.NewInt(0), big.NewInt(23))}
			for i := range replacement {
				replacement[i].BlockHash = "B-2"
			}
			events := decodeLogs(t, "1", replacement...)
			require.NoError(t, sink.StoreWindow(ctx, "1", 4, "B-4", events))
			restarted, err := postgres.NewSink(postgres.SinkConfig{Pool: pool, Handler: DelegationHandler{}, CopyThreshold: threshold})
			require.NoError(t, err)
			require.NoError(t, prepareApplication(ctx, pool))
			require.NoError(t, restarted.StoreWindow(ctx, "1", 4, "B-4", events))
			assertDelegate(t, pool, "1", tokenA, alice, carol)
			assertVotes(t, pool, "1", tokenA, carol, "23")
			height, hash, err = restarted.LoadCursor(ctx, "1")
			require.NoError(t, err)
			assert.Equal(t, uint64(4), height)
			assert.Equal(t, "B-4", hash)
			assert.Equal(t, 7, countRows(t, pool, "chronicle_events", "1"))

			require.NoError(t, restarted.Rollback(ctx, "1", 0, ""))
			for _, table := range []string{"delegation_changes", "delegation_vote_changes", "delegation_current", "delegation_voting_power", "chronicle_events"} {
				assert.Zero(t, countRows(t, pool, table, "1"))
			}
			assertDelegate(t, pool, "2", tokenA, alice, alice)
			assertVotes(t, pool, "2", tokenA, alice, "9")
		})
	}
}

func TestDelegationMalformedEventsAreAtomic(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			pool, sink := setup(t, threshold)
			cases := []struct {
				name  string
				kind  string
				field string
				value any
			}{
				{"missing delegator", "DelegateChanged", "delegator", nil},
				{"wrong delegate type", "DelegateChanged", "toDelegate", 7},
				{"missing previous votes", "DelegateVotesChanged", "previousVotes", nil},
				{"malformed votes", "DelegateVotesChanged", "newVotes", "invalid"},
				{"negative votes", "DelegateVotesChanged", "newVotes", big.NewInt(-1)},
				{"overflow", "DelegateVotesChanged", "newVotes", new(big.Int).Lsh(big.NewInt(1), 256)},
				{"negative previous votes", "DelegateVotesChanged", "previousVotes", big.NewInt(-1)},
				{"unexpected event", "Transfer", "unused", nil},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					events := decodeLogs(t, "1", changeLog(1, 0, alice, zero, bob), votesLog(1, 1, bob, big.NewInt(0), big.NewInt(10)))
					bad := &events[1]
					bad.EventType = tc.kind
					if tc.kind == "DelegateChanged" {
						bad.Fields = types.EventFields{"delegator": alice, "fromDelegate": zero, "toDelegate": bob}
					}
					if tc.value == nil {
						delete(bad.Fields, tc.field)
					} else {
						bad.Fields[tc.field] = tc.value
					}
					assert.Error(t, sink.Store(context.Background(), events))
					for _, table := range []string{"delegation_changes", "delegation_vote_changes", "chronicle_events", "chronicle_cursors"} {
						assert.Zero(t, countRows(t, pool, table, "1"))
					}
				})
			}
		})
	}
}

func TestDelegationFailedRollbackIsAtomic(t *testing.T) {
	pool, sink := setup(t, 1)
	ctx := context.Background()
	require.NoError(t, sink.Store(ctx, decodeLogs(t, "1", changeLog(1, 0, alice, zero, bob), votesLog(1, 1, bob, big.NewInt(0), big.NewInt(10)))))
	_, err := pool.Exec(ctx, "ALTER TABLE chronicle_cursors ADD CHECK(block_num > 0)")
	require.NoError(t, err)
	assert.Error(t, sink.Rollback(ctx, "1", 1, ""))
	assertDelegate(t, pool, "1", tokenA, alice, bob)
	assertVotes(t, pool, "1", tokenA, bob, "10")
	assert.Equal(t, 2, countRows(t, pool, "chronicle_events", "1"))
	height, hash, err := sink.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), height)
	assert.Equal(t, "A-1", hash)
}

func TestDelegationLeaderboardAndHistoricalVotes(t *testing.T) {
	pool, sink := setup(t, 1)
	ctx := context.Background()
	logs := []godex.Log{
		changeLog(1, 0, alice, zero, bob), votesLog(1, 1, bob, big.NewInt(0), big.NewInt(20)),
		changeLog(1, 2, carol, zero, bob), votesLog(1, 3, bob, big.NewInt(20), big.NewInt(30)),
		changeLog(1, 4, bob, zero, bob), votesLog(1, 5, bob, big.NewInt(30), big.NewInt(35)),
		votesLog(1, 6, bob, big.NewInt(0), big.NewInt(999)),
		changeLog(2, 0, alice, bob, carol), votesLog(2, 1, bob, big.NewInt(35), big.NewInt(15)),
		votesLog(2, 2, carol, big.NewInt(0), big.NewInt(20)),
	}
	logs[6].Address = tokenB
	require.NoError(t, sink.Store(ctx, decodeLogs(t, "1", logs...)))
	rows, err := pool.Query(ctx, `SELECT p.delegate, p.votes::text, count(d.delegator)
		FROM delegation_voting_power p LEFT JOIN delegation_current d
		ON d.chain_id=p.chain_id AND d.contract_address=p.contract_address AND d.delegate=p.delegate
		WHERE p.chain_id=$1 AND p.contract_address=$2 AND p.votes>0 AND p.delegate<>$3
		GROUP BY p.delegate,p.votes ORDER BY p.votes DESC,p.delegate LIMIT 20`, "1", tokenA, zero)
	require.NoError(t, err)
	type ranking struct {
		delegate, votes string
		delegators      int
	}
	ranks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ranking, error) {
		var rank ranking
		err := row.Scan(&rank.delegate, &rank.votes, &rank.delegators)
		return rank, err
	})
	require.NoError(t, err)
	assert.Equal(t, []ranking{{carol, "20", 1}, {bob, "15", 2}}, ranks)
	var votes string
	require.NoError(t, pool.QueryRow(ctx, `SELECT new_votes::text FROM delegation_vote_changes
		WHERE chain_id=$1 AND contract_address=$2 AND delegate=$3 AND block_num<=$4
		ORDER BY block_num DESC,log_index DESC LIMIT 1`, "1", tokenA, bob, 1).Scan(&votes))
	assert.Equal(t, "35", votes, "historical balances use the final log of the block")
}
