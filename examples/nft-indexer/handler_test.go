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
	"github.com/ryuux05/godex/pkg/godex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	collectionA   = "0x1111111111111111111111111111111111111111"
	collectionB   = "0x2222222222222222222222222222222222222222"
	alice         = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bob           = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	carol         = "0xcccccccccccccccccccccccccccccccccccccccc"
	zero          = string(types.ZeroAddress)
	transferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
)

func transferLog(block, index uint64, from, to string, id *big.Int) godex.Log {
	return godex.Log{
		Address: collectionA,
		Topics: []string{transferTopic, "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(from, "0x"),
			"0x" + strings.Repeat("0", 24) + strings.TrimPrefix(to, "0x"), fmt.Sprintf("0x%064x", id)},
		Data: "0x", BlockNumber: fmt.Sprintf("0x%x", block), BlockHash: fmt.Sprintf("A-%d", block),
		TransactionHash: fmt.Sprintf("tx-%d", block), LogIndex: fmt.Sprintf("0x%x", index),
	}
}

func decodeTransfers(t *testing.T, chain string, logs ...godex.Log) []godex.Event {
	t.Helper()
	d := decoder.NewStandardDecoder()
	require.NoError(t, d.RegisterABI("nft", erc721ABI))
	events := make([]godex.Event, 0, len(logs))
	for _, l := range logs {
		l.TransactionHash += "-chain-" + chain
		event, err := d.Decode("nft", chain, l)
		require.NoError(t, err)
		require.NotNil(t, event)
		events = append(events, *event)
	}
	return events
}

func setup(t *testing.T, threshold int) (*pgxpool.Pool, *postgres.PGSink) {
	t.Helper()
	pool := testutil.Postgres(t)
	sink, err := postgres.NewSink(postgres.SinkConfig{Pool: pool, Handler: NFTHandler{}, CopyThreshold: threshold})
	require.NoError(t, err)
	require.NoError(t, prepareApplication(context.Background(), pool))
	return pool, sink
}

func assertOwner(t *testing.T, pool *pgxpool.Pool, chain, collection, id, want string) {
	t.Helper()
	var owner string
	err := pool.QueryRow(context.Background(), `SELECT owner FROM nft_owners
		WHERE chain_id=$1 AND contract_address=$2 AND token_id=$3::numeric`, chain, collection, id).Scan(&owner)
	if want == zero {
		require.ErrorIs(t, err, pgx.ErrNoRows, "burned tokens must have no current owner")
	} else {
		require.NoError(t, err)
		assert.Equal(t, want, owner)
	}
}

func assertBalance(t *testing.T, pool *pgxpool.Pool, chain, collection, owner string, want int) {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT COALESCE(
		(SELECT token_count FROM nft_wallet_balances WHERE chain_id=$1 AND contract_address=$2 AND owner=$3),0)`,
		chain, collection, owner).Scan(&count))
	assert.Equal(t, want, count)
}

func countRows(t *testing.T, pool *pgxpool.Pool, table, chain string) int {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE chain_id=$1", chain).Scan(&count))
	return count
}

func TestNFTLifecycleReplayRollbackAndRestart(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			pool, sink := setup(t, threshold)
			ctx := context.Background()
			id := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
			store := func(logs ...godex.Log) {
				events := decodeTransfers(t, "1", logs...)
				for i := 0; i < 2; i++ {
					require.NoError(t, sink.Store(ctx, events))
				}
			}
			store(transferLog(1, 0, zero, alice, id), transferLog(1, 1, zero, alice, big.NewInt(0)))
			assertOwner(t, pool, "1", collectionA, id.String(), alice)
			assertOwner(t, pool, "1", collectionA, "0", alice)
			assertBalance(t, pool, "1", collectionA, alice, 2)
			assertBalance(t, pool, "1", collectionA, zero, 0)

			store(transferLog(2, 0, alice, bob, id), transferLog(2, 1, bob, bob, id), transferLog(2, 2, alice, carol, big.NewInt(0)))
			assertOwner(t, pool, "1", collectionA, id.String(), bob)
			assertBalance(t, pool, "1", collectionA, bob, 1)
			assertBalance(t, pool, "1", collectionA, alice, 0)
			assertBalance(t, pool, "1", collectionA, carol, 1)
			assert.Equal(t, 5, countRows(t, pool, "nft_transfers", "1"), "same-transaction transfers remain separate")

			store(transferLog(3, 0, bob, zero, id))
			assertOwner(t, pool, "1", collectionA, id.String(), zero)
			assertBalance(t, pool, "1", collectionA, bob, 0)
			assertBalance(t, pool, "1", collectionA, zero, 0)
			assert.Equal(t, 1, countRows(t, pool, "nft_owners", "1"))
			assert.Equal(t, 2, countRows(t, pool, "nft_token_state", "1"), "burn tombstones remain queryable")

			store(transferLog(4, 0, zero, alice, id))
			assertOwner(t, pool, "1", collectionA, id.String(), alice)
			require.NoError(t, sink.Rollback(ctx, "1", 4, "A-3"))
			assertOwner(t, pool, "1", collectionA, id.String(), zero)
			require.NoError(t, sink.Rollback(ctx, "1", 3, "A-2"))
			assertOwner(t, pool, "1", collectionA, id.String(), bob)
			assertBalance(t, pool, "1", collectionA, bob, 1)

			replacement := []godex.Log{transferLog(3, 0, bob, carol, id), transferLog(3, 1, carol, alice, big.NewInt(0))}
			for i := range replacement {
				replacement[i].BlockHash = "B-3"
			}
			events := decodeTransfers(t, "1", replacement...)
			require.NoError(t, sink.StoreWindow(ctx, "1", 5, "B-5", events))
			restarted, err := postgres.NewSink(postgres.SinkConfig{Pool: pool, Handler: NFTHandler{}, CopyThreshold: threshold})
			require.NoError(t, err)
			require.NoError(t, prepareApplication(ctx, pool))
			require.NoError(t, restarted.StoreWindow(ctx, "1", 5, "B-5", events))
			assertOwner(t, pool, "1", collectionA, id.String(), carol)
			assertOwner(t, pool, "1", collectionA, "0", alice)
			assertBalance(t, pool, "1", collectionA, bob, 0)
			assertBalance(t, pool, "1", collectionA, carol, 1)
			assert.Equal(t, 7, countRows(t, pool, "nft_transfers", "1"))
			height, hash, err := restarted.LoadCursor(ctx, "1")
			require.NoError(t, err)
			assert.Equal(t, uint64(5), height)
			assert.Equal(t, "B-5", hash)

			var owner string
			var index int
			require.NoError(t, pool.QueryRow(ctx, `SELECT to_address,log_index FROM nft_transfers
				WHERE chain_id='1' AND contract_address=$1 AND token_id=$2::numeric AND block_num<=2
				ORDER BY block_num DESC,log_index DESC LIMIT 1`, collectionA, id.String()).Scan(&owner, &index))
			assert.Equal(t, bob, owner)
			assert.Equal(t, 1, index, "historical ownership uses the last transfer of the block")
		})
	}
}

func TestNFTRollbackIsolatesChainsAndCollections(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			pool, sink := setup(t, threshold)
			ctx := context.Background()
			otherMint := transferLog(1, 1, zero, bob, big.NewInt(1))
			otherMint.Address = collectionB
			otherBurn := transferLog(2, 1, bob, zero, big.NewInt(1))
			otherBurn.Address = collectionB
			require.NoError(t, sink.Store(ctx, decodeTransfers(t, "1", transferLog(1, 0, zero, alice, big.NewInt(1)), otherMint,
				transferLog(2, 0, alice, carol, big.NewInt(1)), otherBurn)))
			require.NoError(t, sink.Store(ctx, decodeTransfers(t, "2", transferLog(2, 0, zero, bob, big.NewInt(1)))))
			assertOwner(t, pool, "1", collectionA, "1", carol)
			assertOwner(t, pool, "1", collectionB, "1", zero)
			require.NoError(t, sink.Rollback(ctx, "1", 2, "A-1"))
			assertOwner(t, pool, "1", collectionA, "1", alice)
			assertOwner(t, pool, "1", collectionB, "1", bob)
			assertOwner(t, pool, "2", collectionA, "1", bob)
			require.NoError(t, sink.Rollback(ctx, "1", 0, ""))
			for _, table := range []string{"nft_transfers", "nft_token_state", "nft_owners", "nft_wallet_balances", "chronicle_events"} {
				assert.Zero(t, countRows(t, pool, table, "1"))
			}
			assertOwner(t, pool, "2", collectionA, "1", bob)
			assertBalance(t, pool, "2", collectionA, bob, 1)
		})
	}
}

func TestNFTMalformedEventsAreAtomic(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			pool, sink := setup(t, threshold)
			for _, tc := range []struct {
				name, field string
				value       any
			}{
				{"missing sender", "from", nil},
				{"wrong recipient type", "to", 5},
				{"missing ID", "tokenId", nil},
				{"malformed ID", "tokenId", "invalid"},
				{"negative ID", "tokenId", big.NewInt(-1)},
				{"overflow ID", "tokenId", new(big.Int).Lsh(big.NewInt(1), 256)},
				{"unsupported event", "", nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					events := decodeTransfers(t, "1", transferLog(1, 0, zero, alice, big.NewInt(1)), transferLog(1, 1, alice, bob, big.NewInt(1)))
					if tc.field == "" {
						events[1].EventType = "Approval"
					} else if tc.value == nil {
						delete(events[1].Fields, tc.field)
					} else {
						events[1].Fields[tc.field] = tc.value
					}
					assert.Error(t, sink.Store(context.Background(), events))
					for _, table := range []string{"nft_transfers", "nft_owners", "chronicle_events", "chronicle_cursors"} {
						assert.Zero(t, countRows(t, pool, table, "1"))
					}
				})
			}
		})
	}
}

func TestNFTFailedRollbackIsAtomic(t *testing.T) {
	pool, sink := setup(t, 1)
	ctx := context.Background()
	require.NoError(t, sink.Store(ctx, decodeTransfers(t, "1", transferLog(1, 0, zero, alice, big.NewInt(1)))))
	_, err := pool.Exec(ctx, "ALTER TABLE chronicle_cursors ADD CHECK(block_num>0)")
	require.NoError(t, err)
	assert.Error(t, sink.Rollback(ctx, "1", 1, ""))
	assertOwner(t, pool, "1", collectionA, "1", alice)
	assertBalance(t, pool, "1", collectionA, alice, 1)
	assert.Equal(t, 1, countRows(t, pool, "chronicle_events", "1"))
	height, hash, err := sink.LoadCursor(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), height)
	assert.Equal(t, "A-1", hash)
}

func TestNFTABIRequiresIndexedTokenID(t *testing.T) {
	d := decoder.NewStandardDecoder()
	require.NoError(t, d.RegisterABI("nft", erc721ABI))
	l := transferLog(1, 0, alice, bob, big.NewInt(7))
	l.Topics = l.Topics[:3]
	l.Data = fmt.Sprintf("0x%064x", 7)
	_, err := d.Decode("nft", "1", l)
	require.Error(t, err, "ERC20-style unindexed token amounts are not ERC721 token IDs")
}
