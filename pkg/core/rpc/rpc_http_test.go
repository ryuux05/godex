package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHead_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "0x10d4f",
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := rpc.Head(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "0x10d4f", got)
}

func TestHead_RPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]any{
				"code":    -32000,
				"message": "oops",
			},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := rpc.Head(ctx)
	assert.Error(t, err)
}

func TestHead_HTTPStatuNotOk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := rpc.Head(ctx)
	assert.Error(t, err)
}

func TestGetBlock_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"Number":     "0x3039",
				"Hash":       "0xabc",
				"ParentHash": "0xdef",
				"Timestamp":  "1700000000",
			},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := rpc.GetBlock(ctx, "0x3039") // "0x3039" == 12345
	assert.NoError(t, err)
	assert.Equal(t, "0x3039", got.Number)
	assert.Equal(t, "0xabc", got.Hash)
	assert.Equal(t, "0xdef", got.ParentHash)
	assert.Equal(t, "1700000000", got.Timestamp)
}

func TestGetBlock_RPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]any{
				"code":    -32000,
				"message": "oops",
			},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := rpc.GetBlock(ctx, "latest")
	assert.Error(t, err)
}

func TestGetBlock_HTTPStatusNotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := rpc.GetBlock(ctx, "latest")
	assert.Error(t, err)
}

func TestGetLogs_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": []map[string]any{
				{
					"Address":          "0xabc",
					"Topics":           []any{"0xddf252ad"},
					"Data":             "0x01",
					"BlockNumber":      "0x1",
					"TransactionHash":  "0xth1",
					"TransactionIndex": "0",
					"BlockHash":        "0xbh1",
					"LogIndex":         "0x0",
					"Removed":          false,
				},
			},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	filter := types.Filter{
		FromBlock: "0x1",
		ToBlock:   "0x2",
		Address:   []string{"0xabc"},
		Topics:    [][]string{{"0xddf252ad"}},
	}
	logs, err := rpc.GetLogs(ctx, filter)
	assert.NoError(t, err)
	assert.Len(t, logs, 1)
	assert.Equal(t, "0xabc", logs[0].Address)
	assert.Equal(t, []string{"0xddf252ad"}, logs[0].Topics)
	assert.Equal(t, "0x1", logs[0].BlockNumber)
}

func TestGetLogs_RPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]any{
				"code":    -32000,
				"message": "oops",
			},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := rpc.GetLogs(ctx, types.Filter{})
	assert.Error(t, err)
}

func TestGetLogs_HTTPStatusNotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := rpc.GetLogs(ctx, types.Filter{})
	assert.Error(t, err)
}

func TestGetBlockReceipts_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": []map[string]any{
				// Receipt 1: Regular transaction with logs (e.g., ERC20 transfer)
				{
					"blockHash":         "0xblock123",
					"blockNumber":       "0x1",
					"contractAddress":   nil, // null for non-contract creation
					"cumulativeGasUsed": "0x5208",
					"effectiveGasPrice": "0x3b9aca00",
					"from":              "0xsender123",
					"gasUsed":           "0x5208",
					"logs": []map[string]any{
						{
							"address":          "0xtoken123",
							"topics":           []any{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"}, // Transfer event
							"data":             "0x0000000000000000000000000000000000000000000000000de0b6b3a7640000",
							"blockNumber":      "0x1",
							"transactionHash":  "0xtx123",
							"transactionIndex": "0x0",
							"blockHash":        "0xblock123",
							"logIndex":         "0x0",
							"removed":          false,
						},
					},
					"logsBloom":        "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
					"status":           "0x1", // success
					"to":               "0xtoken123",
					"transactionHash":  "0xtx123",
					"transactionIndex": "0x0",
					"type":             "0x2",
				},
				// Receipt 2: Contract creation
				{
					"blockHash":         "0xblock123",
					"blockNumber":       "0x1",
					"contractAddress":   "0xnewcontract456", // NOT null for contract creation
					"cumulativeGasUsed": "0xa410",
					"effectiveGasPrice": "0x3b9aca00",
					"from":              "0xdeployer789",
					"gasUsed":           "0x5208",
					"logs":              []map[string]any{}, // No logs in this example
					"logsBloom":         "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
					"status":            "0x1",
					"to":                "", // empty for contract creation
					"transactionHash":   "0xtx456",
					"transactionIndex":  "0x1",
					"type":              "0x2",
				},
				// Receipt 3: Failed transaction
				{
					"blockHash":         "0xblock123",
					"blockNumber":       "0x1",
					"contractAddress":   nil,
					"cumulativeGasUsed": "0xf618",
					"effectiveGasPrice": "0x3b9aca00",
					"from":              "0xfailsender",
					"gasUsed":           "0x5208",
					"logs":              []map[string]any{}, // No logs because failed
					"logsBloom":         "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
					"status":            "0x0", // failure
					"to":                "0xreceiver",
					"transactionHash":   "0xtxfail",
					"transactionIndex":  "0x2",
					"type":              "0x2",
				},
			},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	receipts, err := rpc.GetBlockReceipts(ctx, "0x000")
	assert.NoError(t, err)
	assert.Len(t, receipts, 3)

	// Verify first receipt (regular transaction with logs)
	assert.Equal(t, "0xblock123", receipts[0].BlockHash)
	assert.Equal(t, "0x1", receipts[0].BlockNumber)
	assert.Nil(t, receipts[0].ContractAddress) // null for non-contract creation
	assert.Equal(t, "0x1", receipts[0].Status)
	assert.Len(t, receipts[0].Logs, 1)
	assert.Equal(t, "0xtoken123", receipts[0].Logs[0].Address)

	// Verify second receipt (contract creation)
	assert.Equal(t, "0x1", receipts[1].Status)
	assert.NotNil(t, receipts[1].ContractAddress)
	assert.Equal(t, "0xnewcontract456", *receipts[1].ContractAddress)
	assert.Len(t, receipts[1].Logs, 0)

	// Verify third receipt (failed transaction)
	assert.Equal(t, "0x0", receipts[2].Status) // failed
	assert.Len(t, receipts[2].Logs, 0)
}

func TestGetBlockReceipts_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]any{
				"code":    -32000,
				"message": "block not found",
			},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	receipts, err := rpc.GetBlockReceipts(ctx, "0x000")
	assert.Error(t, err)
	assert.Len(t, receipts, 0)
}

func TestGetBlockReceipts_EmptyBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  []map[string]any{},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	receipt, err := rpc.GetBlockReceipts(ctx, "0x000")
	assert.NoError(t, err)
	assert.Len(t, receipt, 0)
}

func TestHttpRateLimit_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "0x10d4f",
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 1, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	laps := make([]time.Duration, 0, 3)
	for i := 0; i < 3; i++ {
		_, err := rpc.Head(ctx)
		assert.NoError(t, err)
		laps = append(laps, time.Since(start))
	}

	require.Less(t, laps[0], laps[1])
	require.Less(t, laps[1], laps[2])

	// First call should be instant
	require.Less(t, laps[0], 100*time.Millisecond)
	// There should be ~1s after struct
	require.Less(t, laps[1], 1100*time.Millisecond)
	// There should be more ~2s after start
	require.Less(t, laps[2], 2100*time.Millisecond)

}

func TestHttpRateLimitBurst_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "0x10d4f",
		})
	}))
	defer srv.Close()
	done := make(chan struct{})

	const workers = 5

	var idx int64

	var wg sync.WaitGroup
	wg.Add(workers)

	rpc := NewHTTPRPC(srv.URL, 3, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	laps := make([]time.Time, 25)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-done
			for j := 0; j < 5; j++ {
				_, err := rpc.Head(ctx)
				assert.NoError(t, err)
				// Make sure each goroutine have unique index
				k := atomic.AddInt64(&idx, 1) - 1
				laps[k] = time.Now()
			}
		}()

	}

	close(done)
	wg.Wait()

	// Sort out the laps
	sort.Slice(laps, func(i, j int) bool {
		return laps[i].Before(laps[j])
	})

	window := time.Second
	maxInWindow := 0

	for i := 0; i < len(laps); i++ {
		start := laps[i]
		end := start.Add(window)

		count := 1
		for j := i + 1; j < len(laps); j++ {
			if laps[j].After(end) {
				break
			}
			count++
		}

		if count > maxInWindow {
			maxInWindow = count
		}
	}

	allowed := 5 + 1
	require.LessOrEqual(t, maxInWindow, allowed)
}

func TestGetBlocks_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's a batch request (array)
		var requests []map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&requests); err != nil {
			t.Fatalf("expected batch request array: %v", err)
		}

		assert.Len(t, requests, 3)
		assert.Equal(t, "eth_getBlockByNumber", requests[0]["method"])

		// Return batch response
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"jsonrpc": "2.0",
				"id":      0,
				"result": map[string]any{
					"number":     "0x1",
					"hash":       "0xblock1",
					"parentHash": "0x0",
					"timestamp":  "0x65f5a000",
				},
			},
			{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"number":     "0x2",
					"hash":       "0xblock2",
					"parentHash": "0xblock1",
					"timestamp":  "0x65f5a00c",
				},
			},
			{
				"jsonrpc": "2.0",
				"id":      2,
				"result": map[string]any{
					"number":     "0x3",
					"hash":       "0xblock3",
					"parentHash": "0xblock2",
					"timestamp":  "0x65f5a018",
				},
			},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	blocks, err := rpc.GetBlocks(ctx, []string{"0x1", "0x2", "0x3"})
	assert.NoError(t, err)
	assert.Len(t, blocks, 3)
	assert.Equal(t, "0xblock1", blocks["0x1"].Hash)
	assert.Equal(t, "0xblock2", blocks["0x2"].Hash)
	assert.Equal(t, "0xblock3", blocks["0x3"].Hash)
	assert.Equal(t, "0x65f5a000", blocks["0x1"].Timestamp)
}

func TestGetBlocks_EmptyInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not make request for empty input")
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	blocks, err := rpc.GetBlocks(ctx, []string{})
	assert.NoError(t, err)
	assert.Len(t, blocks, 0)
}

func TestGetBlocks_PartialError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// One success, one error
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"jsonrpc": "2.0",
				"id":      0,
				"result": map[string]any{
					"number":     "0x1",
					"hash":       "0xblock1",
					"parentHash": "0x0",
					"timestamp":  "0x65f5a000",
				},
			},
			{
				"jsonrpc": "2.0",
				"id":      1,
				"error": map[string]any{
					"code":    -32000,
					"message": "block not found",
				},
			},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	blocks, err := rpc.GetBlocks(ctx, []string{"0x1", "0x999"})
	assert.NoError(t, err)
	assert.Len(t, blocks, 1)
	assert.Equal(t, "0xblock1", blocks["0x1"].Hash)
	_, exists := blocks["0x999"]
	assert.False(t, exists)
}

func TestGetBlocks_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := rpc.GetBlocks(ctx, []string{"0x1", "0x2"})
	assert.Error(t, err)
}

func TestGetBlocks_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not valid json"))
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := rpc.GetBlocks(ctx, []string{"0x1"})
	assert.Error(t, err)
}
