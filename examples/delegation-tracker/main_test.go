package main

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryuux05/godex/internal/testutil"
	"github.com/ryuux05/godex/pkg/godex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunIndexesAndResumes(t *testing.T) {
	pool := testutil.Postgres(t)
	var logRequests atomic.Int32
	var headRequests atomic.Int32
	filters := make(chan godex.Filter, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result any
		switch req.Method {
		case "eth_chainId":
			result = "0x1"
		case "eth_blockNumber":
			headRequests.Add(1)
			result = "0x3"
		case "eth_getBlockByNumber":
			var number string
			_ = json.Unmarshal(req.Params[0], &number)
			n, _ := strconv.ParseUint(strings.TrimPrefix(number, "0x"), 16, 64)
			parent := ""
			if n > 0 {
				parent = "A-" + strconv.FormatUint(n-1, 10)
			}
			result = godex.Block{Number: number, Hash: "A-" + strconv.FormatUint(n, 10), ParentHash: parent}
		case "eth_getLogs":
			logRequests.Add(1)
			var filter godex.Filter
			if err := json.Unmarshal(req.Params[0], &filter); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			select {
			case filters <- filter:
			default:
			}
			from, _ := strconv.ParseUint(strings.TrimPrefix(filter.FromBlock, "0x"), 16, 64)
			to, _ := strconv.ParseUint(strings.TrimPrefix(filter.ToBlock, "0x"), 16, 64)
			logs := []godex.Log{}
			for _, l := range []godex.Log{changeLog(1, 0, alice, zero, bob), votesLog(1, 1, bob, big.NewInt(0), big.NewInt(42))} {
				if from <= 1 && to >= 1 {
					logs = append(logs, l)
				}
			}
			result = logs
		default:
			http.Error(w, "unexpected method "+req.Method, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer srv.Close()
	// ConnString retains the original DSN, without testutil's isolated schema.
	dsn := pool.Config().ConnString()
	schema := pool.Config().ConnConfig.RuntimeParams["search_path"]
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		query := u.Query()
		query.Set("search_path", schema)
		u.RawQuery = query.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("RPC_URL", srv.URL)
	t.Setenv("CHAIN_ID", "1")
	t.Setenv("CONTRACT_ADDRESS", tokenA)
	t.Setenv("FROM_BLOCK", "1")
	t.Setenv("CONFIRMATION_DEPTH", "0")

	for i := 0; i < 2; i++ {
		func() {
			previousHeads := headRequests.Load()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			done := make(chan error, 1)
			go func() { done <- run(ctx) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					assert.NoError(t, err)
				case <-time.After(5 * time.Second):
					t.Error("delegation tracker did not stop")
				}
			}()
			require.Eventually(t, func() bool {
				var height uint64
				err := pool.QueryRow(ctx, "SELECT block_num FROM chronicle_cursors WHERE chain_id='1'").Scan(&height)
				return err == nil && height == 3 && headRequests.Load() >= previousHeads+2
			}, 5*time.Second, 10*time.Millisecond)
		}()
		assertDelegate(t, pool, "1", tokenA, alice, bob)
		assertVotes(t, pool, "1", tokenA, bob, "42")
		assert.Equal(t, 2, countRows(t, pool, "chronicle_events", "1"))
	}
	assert.Equal(t, int32(1), logRequests.Load(), "restart resumes the durable cursor")
	select {
	case filter := <-filters:
		assert.Equal(t, "0x1", filter.FromBlock)
		assert.Equal(t, []string{tokenA}, filter.Address)
		require.Len(t, filter.Topics, 1)
		assert.Len(t, filter.Topics[0], 2)
	default:
		t.Fatal("tracker did not request logs")
	}
}

func TestRunRejectsInvalidEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, value string
	}{
		{"DATABASE_URL", ""},
		{"RPC_URL", ""},
		{"CHAIN_ID", ""},
		{"CONTRACT_ADDRESS", ""},
		{"FROM_BLOCK", ""},
		{"FROM_BLOCK", "-1"},
		{"FROM_BLOCK", "18446744073709551616"},
		{"CONFIRMATION_DEPTH", "bad"},
	} {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			for name, value := range map[string]string{
				"DATABASE_URL": "postgres://unused", "RPC_URL": "http://unused", "CHAIN_ID": "1",
				"CONTRACT_ADDRESS": tokenA, "FROM_BLOCK": "1", "CONFIRMATION_DEPTH": "12",
			} {
				t.Setenv(name, value)
			}
			t.Setenv(tc.name, tc.value)
			assert.ErrorContains(t, run(context.Background()), tc.name)
		})
	}
}
