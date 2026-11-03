package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ryuux05/godex/internal/testutil"
	"github.com/ryuux05/godex/pkg/godex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecipientRPCBatchesSortsAndRejectsPartialResults(t *testing.T) {
	pool := testutil.Postgres(t)
	ctx := context.Background()
	require.NoError(t, prepareApplication(ctx, pool))
	var calls atomic.Int32
	var failSecond atomic.Bool
	var malformed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var filter godex.Filter
		if req.Method != "eth_getLogs" || len(req.Params) != 1 || json.Unmarshal(req.Params[0], &filter) != nil {
			http.Error(w, "invalid log request", 400)
			return
		}
		n := calls.Add(1)
		if failSecond.Load() && n%2 == 0 {
			http.Error(w, "failed recipient batch", 500)
			return
		}
		if len(filter.Topics) != 3 || len(filter.Topics[2]) == 0 || len(filter.Topics[2]) > 128 {
			http.Error(w, "invalid recipient batch", 400)
			return
		}
		if len(filter.Address) != 1 || filter.Address[0] != tokenA || len(filter.Topics[0]) != 1 || filter.Topics[0][0] != transferTopic || len(filter.Topics[1]) != 0 {
			http.Error(w, "wrong payment filter", 400)
			return
		}
		first := 1
		if len(filter.Topics[2]) == 1 {
			first = 129
		}
		for i, topic := range filter.Topics[2] {
			if topic != fmt.Sprintf("0x%064x", first+i) {
				http.Error(w, "wrong invoice recipient", 400)
				return
			}
		}
		recipient := "0x" + filter.Topics[2][0][26:]
		selected := []godex.Log{transferLog(3, 2, payer, recipient, big.NewInt(3)), transferLog(1, 0, payer, recipient, big.NewInt(1))}
		if len(filter.Topics[2]) == 1 {
			selected = []godex.Log{transferLog(2, 0, payer, recipient, big.NewInt(2))}
		}
		if malformed.Load() {
			selected[0].BlockNumber = "bad-height"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": selected})
	}))
	defer srv.Close()
	client, err := godex.NewHTTPRPCWithOptions(srv.URL, godex.HTTPRPCOptions{})
	require.NoError(t, err)
	rpc := &recipientRPC{HTTPRPC: client, pool: pool, chainID: "1", tokenAddress: tokenA}
	filter := godex.Filter{FromBlock: "0x1", ToBlock: "0x5"}
	logs, err := rpc.GetLogs(ctx, filter)
	require.NoError(t, err)
	assert.Empty(t, logs)
	assert.Zero(t, calls.Load(), "no invoices must not trigger an unfiltered scan")
	for i := 1; i <= 129; i++ {
		_, err := registerInvoice(ctx, pool, invoice(fmt.Sprintf("order-%d", i), "1", tokenA, fmt.Sprintf("0x%040x", i), "1", 1))
		require.NoError(t, err)
	}
	for _, i := range []Invoice{
		invoice("future", "1", tokenA, depositA, "1", 9), invoice("other-token", "1", tokenB, depositA, "1", 1), invoice("other-chain", "2", tokenA, depositA, "1", 1),
	} {
		_, err := registerInvoice(ctx, pool, i)
		require.NoError(t, err)
	}
	logs, err = rpc.GetLogs(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load())
	require.Len(t, logs, 3)
	assert.Equal(t, "0x1", logs[0].BlockNumber)
	assert.Equal(t, "0x2", logs[1].BlockNumber)
	assert.Equal(t, "0x3", logs[2].BlockNumber)
	failSecond.Store(true)
	logs, err = rpc.GetLogs(ctx, filter)
	assert.Error(t, err)
	assert.Nil(t, logs, "never return a partial successful window")
	failSecond.Store(false)
	malformed.Store(true)
	logs, err = rpc.GetLogs(ctx, filter)
	assert.Error(t, err)
	assert.Nil(t, logs)
	_, err = rpc.GetLogs(ctx, godex.Filter{ToBlock: "invalid"})
	assert.Error(t, err)
}
