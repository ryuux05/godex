package godex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryuux05/godex/adapters/sink/postgres"
	"github.com/ryuux05/godex/internal/testutil"
	"github.com/ryuux05/godex/pkg/godex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runServiceUntil(t *testing.T, p *godex.Processor, ready func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("service did not stop")
		}
	}()
	require.Eventually(t, ready, 4*time.Second, 10*time.Millisecond)
}

func TestServiceConfigPostgresRestartAndReorg(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			pool := testutil.Postgres(t)
			ctx := context.Background()
			_, err := pool.Exec(ctx, "CREATE TABLE app_transfers(event_id TEXT PRIMARY KEY,chain_id TEXT,block_num BIGINT,value TEXT)")
			require.NoError(t, err)
			var branchB atomic.Bool
			large := new(big.Int).Lsh(big.NewInt(1), 200)
			hash := func(n uint64) string {
				branch := "A"
				if n >= 2 && branchB.Load() {
					branch = "B"
				}
				return fmt.Sprintf("%s-%d", branch, n)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     int               `json:"id"`
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				var result any
				switch req.Method {
				case "eth_chainId":
					result = "0x1"
				case "eth_blockNumber":
					result = "0x3"
				case "eth_getBlockByNumber":
					var number string
					_ = json.Unmarshal(req.Params[0], &number)
					n, _ := strconv.ParseUint(strings.TrimPrefix(number, "0x"), 16, 64)
					parent := ""
					if n > 0 {
						parent = hash(n - 1)
					}
					result = godex.Block{Number: number, Hash: hash(n), ParentHash: parent}
				case "eth_getLogs":
					var filter godex.Filter
					_ = json.Unmarshal(req.Params[0], &filter)
					from, _ := strconv.ParseUint(strings.TrimPrefix(filter.FromBlock, "0x"), 16, 64)
					to, _ := strconv.ParseUint(strings.TrimPrefix(filter.ToBlock, "0x"), 16, 64)
					logs := []godex.Log{}
					for n := uint64(1); n <= 2; n++ {
						if n < from || n > to {
							continue
						}
						value := large
						if n == 2 {
							value = big.NewInt(7)
							if branchB.Load() {
								value = big.NewInt(9)
							}
						}
						logs = append(logs, godex.Log{Address: serviceAddress, Topics: []string{transferTopic, fmt.Sprintf("0x%064x", 1), fmt.Sprintf("0x%064x", 2)}, Data: fmt.Sprintf("0x%064x", value), BlockNumber: fmt.Sprintf("0x%x", n), BlockHash: hash(n), TransactionHash: "tx", LogIndex: "0x0"})
					}
					result = logs
				default:
					http.Error(w, "unexpected method", 400)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
			}))
			defer srv.Close()
			var rolled []uint64
			handler := postgres.HandlerFuncs{
				HandleEvent: func(ctx context.Context, tx pgx.Tx, e godex.Event) error {
					value, err := e.Fields.BigInt("value")
					if err != nil {
						return err
					}
					_, err = tx.Exec(ctx, "INSERT INTO app_transfers VALUES($1,$2,$3,$4)", e.Id, e.ChainId, e.BlockNumber, value.String())
					return err
				},
				RollbackEvents: func(ctx context.Context, tx pgx.Tx, chain string, from uint64) error {
					rolled = append(rolled, from)
					_, err := tx.Exec(ctx, "DELETE FROM app_transfers WHERE chain_id=$1 AND block_num >=$2", chain, from)
					return err
				},
			}
			cfg := simpleConfig(&setupSink{}, nil)
			cfg.Sink = nil
			cfg.RPCURL = srv.URL
			cfg.FromBlock = 1
			cfg.Postgres = &godex.PostgresConfig{Pool: pool, Handler: handler, CopyThreshold: threshold}
			cfg.Options = &godex.Options{RangeSize: 3, FetcherConcurrency: 1, PollInterval: 10 * time.Millisecond}
			require.NoError(t, cfg.Validate())
			first, err := godex.New(ctx, cfg)
			require.NoError(t, err)
			runServiceUntil(t, first, func() bool { return first.Status().Chains["1"].CursorBlock == 3 })
			require.NoError(t, pool.Ping(ctx), "processor must not close the service's pool")
			var value string
			require.NoError(t, pool.QueryRow(ctx, "SELECT value FROM app_transfers WHERE block_num=1").Scan(&value))
			assert.Equal(t, large.String(), value)
			branchB.Store(true)
			resumed, err := godex.New(ctx, cfg)
			require.NoError(t, err)
			assert.Equal(t, uint64(3), resumed.Status().Chains["1"].CursorBlock)
			runServiceUntil(t, resumed, func() bool {
				var value string
				err := pool.QueryRow(ctx, "SELECT value FROM app_transfers WHERE block_num=2").Scan(&value)
				return err == nil && value == "9" && resumed.Status().Chains["1"].CursorHash == "B-3"
			})
			// Restart has no persisted ancestor headers. The existing engine falls
			// back to block 0 and rebuilds inclusively from block 1.
			assert.Equal(t, []uint64{1}, rolled, "rollback receives ancestor + 1, including during conservative restart recovery")
			for _, table := range []string{"app_transfers", "chronicle_events"} {
				var count int
				require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count))
				assert.Equal(t, 2, count)
			}
			require.NoError(t, pool.QueryRow(ctx, "SELECT value FROM app_transfers WHERE block_num=1").Scan(&value))
			assert.Equal(t, large.String(), value)
			cfg.FromBlock = 0
			again, err := godex.New(ctx, cfg)
			require.NoError(t, err)
			assert.Equal(t, uint64(3), again.Status().Chains["1"].CursorBlock)
			assert.Equal(t, "B-3", again.Status().Chains["1"].CursorHash)
		})
	}
}

func TestServiceConfigEventOnlyPostgres(t *testing.T) {
	pool := testutil.Postgres(t)
	ctx := context.Background()
	cfg := simpleConfig(&setupSink{}, &identityRPC{id: "0x1"})
	cfg.Sink = nil
	cfg.Postgres = &godex.PostgresConfig{Pool: pool}
	_, err := godex.New(ctx, cfg)
	require.NoError(t, err)
	var exists bool
	require.NoError(t, pool.QueryRow(ctx, "SELECT to_regclass('chronicle_events') IS NOT NULL").Scan(&exists))
	assert.True(t, exists)
	assert.Nil(t, cfg.Postgres.Handler, "caller configuration must not be mutated")
	require.NoError(t, pool.Ping(ctx))
}
