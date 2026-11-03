package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type rpcRequest struct {
	ID     int               `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

func TestCheckoutCLIAndLiveInvoiceRegistration(t *testing.T) {
	pool := testutil.Postgres(t)
	var head atomic.Uint64
	head.Store(1)
	var headRequests atomic.Int32
	var logRequests atomic.Int32
	logs := []godex.Log{
		transferLog(2, 0, payer, depositA, big.NewInt(40)), transferLog(3, 0, payer, depositA, big.NewInt(60)),
		transferLog(4, 0, payer, depositB, big.NewInt(200)), transferLog(2, 1, payer, payer, big.NewInt(999)),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var result any
		switch req.Method {
		case "eth_chainId":
			result = "0x1"
		case "eth_blockNumber":
			headRequests.Add(1)
			result = fmt.Sprintf("0x%x", head.Load())
		case "eth_getBlockByNumber":
			var number string
			_ = json.Unmarshal(req.Params[0], &number)
			n, _ := strconv.ParseUint(strings.TrimPrefix(number, "0x"), 16, 64)
			parent := ""
			if n > 0 {
				parent = fmt.Sprintf("A-%d", n-1)
			}
			result = godex.Block{Number: number, Hash: fmt.Sprintf("A-%d", n), ParentHash: parent}
		case "eth_getLogs":
			logRequests.Add(1)
			var filter godex.Filter
			if err := json.Unmarshal(req.Params[0], &filter); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if len(filter.Address) != 1 || filter.Address[0] != tokenA || len(filter.Topics) != 3 || len(filter.Topics[0]) != 1 || filter.Topics[0][0] != transferTopic || len(filter.Topics[1]) != 0 || len(filter.Topics[2]) == 0 {
				http.Error(w, "expected token and recipient filters", 400)
				return
			}
			from, _ := strconv.ParseUint(strings.TrimPrefix(filter.FromBlock, "0x"), 16, 64)
			to, _ := strconv.ParseUint(strings.TrimPrefix(filter.ToBlock, "0x"), 16, 64)
			selected := []godex.Log{}
			for _, log := range logs {
				n, _ := strconv.ParseUint(strings.TrimPrefix(log.BlockNumber, "0x"), 16, 64)
				for _, topic := range filter.Topics[2] {
					if n >= from && n <= to && log.Topics[2] == topic {
						selected = append(selected, log)
						break
					}
				}
			}
			result = selected
		default:
			http.Error(w, "unexpected method "+req.Method, 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer srv.Close()
	dsn := pool.Config().ConnString()
	schema := pool.Config().ConnConfig.RuntimeParams["search_path"]
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	for name, value := range map[string]string{"DATABASE_URL": dsn, "RPC_URL": srv.URL, "CHAIN_ID": "1", "CONTRACT_ADDRESS": tokenA, "FROM_BLOCK": "1", "CONFIRMATION_DEPTH": "0"} {
		t.Setenv(name, value)
	}
	ctx := context.Background()
	require.NoError(t, run(ctx, []string{"init"}, io.Discard))
	var output bytes.Buffer
	require.NoError(t, run(ctx, []string{"invoice", "-id", "order-1", "-to", depositA, "-amount", "100"}, &output))
	var created Invoice
	require.NoError(t, json.Unmarshal(output.Bytes(), &created))
	assert.Equal(t, uint64(2), created.AcceptFromBlock)
	assert.Equal(t, "100", created.ExpectedAmount)

	// New invoices must be picked up without restarting the running watcher.
	head.Store(3)
	watchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	done := make(chan error, 1)
	go func() { done <- run(watchCtx, []string{"watch"}, io.Discard) }()
	stopped := false
	defer func() {
		cancel()
		if !stopped {
			select {
			case err := <-done:
				assert.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Error("payment watcher did not stop")
			}
		}
	}()
	require.Eventually(t, func() bool { s, err := readStatus(ctx, pool, "order-1"); return err == nil && s.Status == "paid" }, 5*time.Second, 10*time.Millisecond)
	assertStatus(t, pool, "order-1", "paid", "100", "0", "0", 2)
	output.Reset()
	require.NoError(t, run(ctx, []string{"invoice", "-id", "order-2", "-to", depositB, "-amount", "200"}, &output))
	require.NoError(t, json.Unmarshal(output.Bytes(), &created))
	assert.Equal(t, uint64(4), created.AcceptFromBlock)
	assertStatus(t, pool, "order-2", "pending", "0", "200", "0", 0)
	head.Store(5)
	require.Eventually(t, func() bool {
		var height uint64
		err := pool.QueryRow(ctx, "SELECT block_num FROM chronicle_cursors WHERE chain_id='1'").Scan(&height)
		s, statusErr := readStatus(ctx, pool, "order-2")
		return err == nil && statusErr == nil && height == 5 && s.Status == "paid"
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
		stopped = true
	case <-time.After(5 * time.Second):
		t.Fatal("payment watcher did not stop")
	}
	output.Reset()
	require.NoError(t, run(ctx, []string{"status", "-id", "order-2"}, &output))
	var paid InvoiceStatus
	require.NoError(t, json.Unmarshal(output.Bytes(), &paid))
	assert.Equal(t, "paid", paid.Status)
	assert.Equal(t, "200", paid.ReceivedAmount)
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM chronicle_events").Scan(&count))
	assert.Equal(t, 3, count, "unwatched recipients must not be indexed")

	beforeLogs, beforeHeads := logRequests.Load(), headRequests.Load()
	func() {
		restartCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		resumed := make(chan error, 1)
		go func() { resumed <- run(restartCtx, nil, io.Discard) }()
		defer func() {
			stop()
			select {
			case err := <-resumed:
				assert.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Error("restart did not stop")
			}
		}()
		require.Eventually(t, func() bool { return headRequests.Load() >= beforeHeads+2 }, 5*time.Second, 10*time.Millisecond)
	}()
	assert.Equal(t, beforeLogs, logRequests.Load(), "restart must resume the persisted cursor")
	assertStatus(t, pool, "order-1", "paid", "100", "0", "0", 2)

	t.Setenv("CHAIN_ID", "2")
	assert.ErrorContains(t, run(ctx, []string{"invoice", "-id", "wrong-chain", "-to", payer, "-amount", "1"}, io.Discard), "chain ID")
	_, err := readStatus(ctx, pool, "wrong-chain")
	assert.Error(t, err)
}

func TestCheckoutRejectsInvalidCommandsBeforeIO(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://unused")
	for _, args := range [][]string{
		{"unknown"}, {"watch", "unexpected"}, {"init", "unexpected"}, {"status"}, {"status", "-id", "order", "extra"},
		{"invoice", "-id", "order", "-to", zero, "-amount", "1"},
		{"invoice", "-id", "order", "-to", depositA, "-amount", "1.5"},
		{"invoice", "-id", "order", "-to", depositA, "-amount", "0"},
		{"invoice", "-id", "order", "-to", depositA, "-amount", "-1"},
		{"invoice", "-id", "order", "-to", depositA, "-amount", new(big.Int).Lsh(big.NewInt(1), 256).String()},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) { assert.Error(t, run(context.Background(), args, io.Discard)) })
	}
	for _, text := range []string{"", "-1", "1e18", "0x", "+1", "foo"} {
		_, err := canonicalChainID(text)
		assert.Error(t, err)
	}
	chain, err := canonicalChainID("0xA")
	require.NoError(t, err)
	assert.Equal(t, "10", chain)
	address, err := parseAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	require.NoError(t, err)
	assert.Equal(t, depositA, address)
	amount, err := parseAmount("000100")
	require.NoError(t, err)
	assert.Equal(t, "100", amount)
}
