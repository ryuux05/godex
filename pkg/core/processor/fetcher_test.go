package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	coreerrors "github.com/ryuux05/godex/pkg/core/errors"
	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createMockRPCForSplit creates an RPC server that returns "response too big" errors
// for ranges specified in tooBigRanges, and successful responses for other ranges
func createMockRPCForSplit(t *testing.T, tooBigRanges map[string]bool, logsByRange map[string][]types.Log) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req struct {
			Method string        `json:"method"`
			Params []interface{} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		switch req.Method {
		case "eth_getLogs":
			// Extract range from params
			params := req.Params[0].(map[string]interface{})
			fromBlock := params["fromBlock"].(string)
			toBlock := params["toBlock"].(string)

			// Create range key
			rangeKey := fmt.Sprintf("%s-%s", fromBlock, toBlock)

			// Check if this range should return "too big" error
			if tooBigRanges[rangeKey] {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"error": map[string]any{
						"code":    -32008,
						"message": "response is too big",
					},
				})
				return
			}

			// Return logs for this range
			logs := logsByRange[rangeKey]
			if logs == nil {
				logs = []types.Log{}
			}

			result := make([]map[string]any, len(logs))
			for i, log := range logs {
				result[i] = map[string]any{
					"Address":          log.Address,
					"Topics":           log.Topics,
					"Data":             log.Data,
					"BlockNumber":      log.BlockNumber,
					"TransactionHash":  log.TransactionHash,
					"TransactionIndex": log.TransactionIndex,
					"BlockHash":        log.BlockHash,
					"LogIndex":         log.LogIndex,
					"Removed":          log.Removed,
				}
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result":  result,
			})

		default:
			http.Error(w, "method not supported", http.StatusBadRequest)
		}
	}))
}

func TestFetchWithSplit_SuccessfulSplit(t *testing.T) {
	// Range 0-7 is too big, but 0-3, 4-7 should succeed
	tooBigRanges := map[string]bool{
		utils.Uint64ToHexQty(0) + "-" + utils.Uint64ToHexQty(7): true,
	}

	// Create logs for split ranges
	logsByRange := map[string][]types.Log{
		utils.Uint64ToHexQty(0) + "-" + utils.Uint64ToHexQty(3): {
			{
				Address:          "0xabc",
				Topics:           []string{"0xddf252ad"},
				BlockNumber:      "0x1",
				TransactionHash:  "0xtx1",
				TransactionIndex: "0x0",
				BlockHash:        "0xbh1",
				LogIndex:         "0x0",
				Removed:          false,
			},
			{
				Address:          "0xdef",
				Topics:           []string{"0xddf252ad"},
				BlockNumber:      "0x2",
				TransactionHash:  "0xtx2",
				TransactionIndex: "0x0",
				BlockHash:        "0xbh2",
				LogIndex:         "0x0",
				Removed:          false,
			},
		},
		utils.Uint64ToHexQty(4) + "-" + utils.Uint64ToHexQty(7): {
			{
				Address:          "0xghi",
				Topics:           []string{"0xddf252ad"},
				BlockNumber:      "0x5",
				TransactionHash:  "0xtx3",
				TransactionIndex: "0x0",
				BlockHash:        "0xbh5",
				LogIndex:         "0x0",
				Removed:          false,
			},
		},
	}

	srv := createMockRPCForSplit(t, tooBigRanges, logsByRange)
	defer srv.Close()

	rpcClient := rpc.NewHTTPRPC(srv.URL, 1000, 1000)
	processor := NewProcessor(nil, &NoopSink{})

	// Create chain state
	chain := &chainState{
		chainInfo: ChainInfo{
			ChainId: "1",
			RPC:     rpcClient,
		},
		topics:    [][]string{{"0xddf252ad"}},
		addresses: []string{},
		opts: &Options{
			FetchMode: FetchModeLogs,
		},
	}

	ctx := context.Background()
	job := BlockRange{From: 0, To: 7}

	logs, err := processor.fetchWithSplit(ctx, chain, job)
	require.NoError(t, err)
	assert.Len(t, logs, 3, "Should have 3 logs from split ranges")

	// Verify logs are in order (block order preserved)
	assert.Equal(t, "0x1", logs[0].BlockNumber)
	assert.Equal(t, "0x2", logs[1].BlockNumber)
	assert.Equal(t, "0x5", logs[2].BlockNumber)
}

func TestFetchWithSplit_MultipleSplits(t *testing.T) {
	// Range 0-15 is too big, 0-7 is too big, 8-15 is too big, but smaller ranges succeed
	tooBigRanges := map[string]bool{
		utils.Uint64ToHexQty(0) + "-" + utils.Uint64ToHexQty(15): true,
		utils.Uint64ToHexQty(0) + "-" + utils.Uint64ToHexQty(7):  true,
		utils.Uint64ToHexQty(8) + "-" + utils.Uint64ToHexQty(15): true, // Add this line
	}

	logsByRange := map[string][]types.Log{
		utils.Uint64ToHexQty(0) + "-" + utils.Uint64ToHexQty(3): {
			{BlockNumber: "0x1", TransactionHash: "0xtx1", LogIndex: "0x0"},
		},
		utils.Uint64ToHexQty(4) + "-" + utils.Uint64ToHexQty(7): {
			{BlockNumber: "0x5", TransactionHash: "0xtx2", LogIndex: "0x0"},
		},
		utils.Uint64ToHexQty(8) + "-" + utils.Uint64ToHexQty(11): {
			{BlockNumber: "0x9", TransactionHash: "0xtx3", LogIndex: "0x0"},
		},
		utils.Uint64ToHexQty(12) + "-" + utils.Uint64ToHexQty(15): {
			{BlockNumber: "0x13", TransactionHash: "0xtx4", LogIndex: "0x0"},
		},
	}

	srv := createMockRPCForSplit(t, tooBigRanges, logsByRange)
	defer srv.Close()

	rpcClient := rpc.NewHTTPRPC(srv.URL, 1000, 1000)
	processor := NewProcessor(nil, &NoopSink{})

	chain := &chainState{
		chainInfo: ChainInfo{
			ChainId: "1",
			RPC:     rpcClient,
		},
		topics:    [][]string{{"0xddf252ad"}},
		addresses: []string{},
		opts: &Options{
			FetchMode: FetchModeLogs,
		},
	}

	ctx := context.Background()
	job := BlockRange{From: 0, To: 15}

	logs, err := processor.fetchWithSplit(ctx, chain, job)
	require.NoError(t, err)
	assert.Len(t, logs, 4, "Should have 4 logs from multiple splits")
}

func TestFetchWithSplit_SingleBlockTooBig(t *testing.T) {
	// Single block 5 is too big
	tooBigRanges := map[string]bool{
		utils.Uint64ToHexQty(5) + "-" + utils.Uint64ToHexQty(5): true,
	}

	srv := createMockRPCForSplit(t, tooBigRanges, map[string][]types.Log{})
	defer srv.Close()

	rpcClient := rpc.NewHTTPRPC(srv.URL, 1000, 1000)
	processor := NewProcessor(nil, &NoopSink{})

	chain := &chainState{
		chainInfo: ChainInfo{
			ChainId: "1",
			RPC:     rpcClient,
		},
		topics:    [][]string{{"0xddf252ad"}},
		addresses: []string{},
		opts: &Options{
			FetchMode: FetchModeLogs,
		},
	}

	ctx := context.Background()
	job := BlockRange{From: 5, To: 5}

	logs, err := processor.fetchWithSplit(ctx, chain, job)
	assert.Error(t, err)
	assert.Nil(t, logs)
	assert.Contains(t, err.Error(), "response too big even for single block")
	assert.Contains(t, err.Error(), "5")
}

func TestFetchWithSplit_ContextCancellation(t *testing.T) {
	// Range 0-7 is too big
	tooBigRanges := map[string]bool{
		utils.Uint64ToHexQty(0) + "-" + utils.Uint64ToHexQty(7): true,
	}

	srv := createMockRPCForSplit(t, tooBigRanges, map[string][]types.Log{})
	defer srv.Close()

	rpcClient := rpc.NewHTTPRPC(srv.URL, 1000, 1000)
	processor := NewProcessor(nil, &NoopSink{})

	chain := &chainState{
		chainInfo: ChainInfo{
			ChainId: "1",
			RPC:     rpcClient,
		},
		topics:    [][]string{{"0xddf252ad"}},
		addresses: []string{},
		opts: &Options{
			FetchMode: FetchModeLogs,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately to test context handling
	cancel()

	job := BlockRange{From: 0, To: 7}

	logs, err := processor.fetchWithSplit(ctx, chain, job)
	assert.Error(t, err)
	assert.Nil(t, logs)
	assert.Equal(t, context.Canceled, err)
}

func TestFetchWithSplit_NonResponseTooBigError(t *testing.T) {
	// Create a server that returns a different error (not "response too big")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		if req.Method == "eth_getLogs" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"error": map[string]any{
					"code":    -32000,
					"message": "server error",
				},
			})
			return
		}

		http.Error(w, "method not supported", http.StatusBadRequest)
	}))
	defer srv.Close()

	rpcClient := rpc.NewHTTPRPC(srv.URL, 1000, 1000)
	processor := NewProcessor(nil, &NoopSink{})

	chain := &chainState{
		chainInfo: ChainInfo{
			ChainId: "1",
			RPC:     rpcClient,
		},
		topics:    [][]string{{"0xddf252ad"}},
		addresses: []string{},
		opts: &Options{
			FetchMode: FetchModeLogs,
		},
	}

	ctx := context.Background()
	job := BlockRange{From: 0, To: 7}

	logs, err := processor.fetchWithSplit(ctx, chain, job)
	assert.Error(t, err)
	assert.Nil(t, logs)
	// Should not be a "response too big" error
	assert.False(t, coreerrors.IsResponseTooBigError(err))
}

func TestFetchWithSplit_NoSplitsNeeded(t *testing.T) {
	// No ranges are too big, should succeed directly
	tooBigRanges := map[string]bool{}

	logsByRange := map[string][]types.Log{
		utils.Uint64ToHexQty(0) + "-" + utils.Uint64ToHexQty(7): {
			{
				Address:          "0xabc",
				Topics:           []string{"0xddf252ad"},
				BlockNumber:      "0x1",
				TransactionHash:  "0xtx1",
				TransactionIndex: "0x0",
				BlockHash:        "0xbh1",
				LogIndex:         "0x0",
				Removed:          false,
			},
		},
	}

	srv := createMockRPCForSplit(t, tooBigRanges, logsByRange)
	defer srv.Close()

	rpcClient := rpc.NewHTTPRPC(srv.URL, 1000, 1000)
	processor := NewProcessor(nil, &NoopSink{})

	chain := &chainState{
		chainInfo: ChainInfo{
			ChainId: "1",
			RPC:     rpcClient,
		},
		topics:    [][]string{{"0xddf252ad"}},
		addresses: []string{},
		opts: &Options{
			FetchMode: FetchModeLogs,
		},
	}

	ctx := context.Background()
	job := BlockRange{From: 0, To: 7}

	logs, err := processor.fetchWithSplit(ctx, chain, job)
	require.NoError(t, err)
	assert.Len(t, logs, 1, "Should have 1 log without splitting")
}

func TestFetchWithSplit_EmptyResult(t *testing.T) {
	// Range succeeds but returns no logs
	tooBigRanges := map[string]bool{}

	logsByRange := map[string][]types.Log{
		utils.Uint64ToHexQty(0) + "-" + utils.Uint64ToHexQty(7): {},
	}

	srv := createMockRPCForSplit(t, tooBigRanges, logsByRange)
	defer srv.Close()

	rpcClient := rpc.NewHTTPRPC(srv.URL, 1000, 1000)
	processor := NewProcessor(nil, &NoopSink{})

	chain := &chainState{
		chainInfo: ChainInfo{
			ChainId: "1",
			RPC:     rpcClient,
		},
		topics:    [][]string{{"0xddf252ad"}},
		addresses: []string{},
		opts: &Options{
			FetchMode: FetchModeLogs,
		},
	}

	ctx := context.Background()
	job := BlockRange{From: 0, To: 7}

	logs, err := processor.fetchWithSplit(ctx, chain, job)
	require.NoError(t, err)
	assert.Len(t, logs, 0, "Should return empty logs")
}

func TestFetchWithSplit_OrderPreservation(t *testing.T) {
	// Test that logs are returned in block order even after splitting
	tooBigRanges := map[string]bool{
		utils.Uint64ToHexQty(0) + "-" + utils.Uint64ToHexQty(9): true,
	}

	// Create logs in different ranges, out of order
	logsByRange := map[string][]types.Log{
		utils.Uint64ToHexQty(0) + "-" + utils.Uint64ToHexQty(4): {
			{BlockNumber: "0x2", TransactionHash: "0xtx2", LogIndex: "0x0"},
			{BlockNumber: "0x4", TransactionHash: "0xtx4", LogIndex: "0x0"},
		},
		utils.Uint64ToHexQty(5) + "-" + utils.Uint64ToHexQty(9): {
			{BlockNumber: "0x5", TransactionHash: "0xtx5", LogIndex: "0x0"},
			{BlockNumber: "0x7", TransactionHash: "0xtx7", LogIndex: "0x0"},
			{BlockNumber: "0x9", TransactionHash: "0xtx9", LogIndex: "0x0"},
		},
	}

	srv := createMockRPCForSplit(t, tooBigRanges, logsByRange)
	defer srv.Close()

	rpcClient := rpc.NewHTTPRPC(srv.URL, 1000, 1000)
	processor := NewProcessor(nil, &NoopSink{})

	chain := &chainState{
		chainInfo: ChainInfo{
			ChainId: "1",
			RPC:     rpcClient,
		},
		topics:    [][]string{{"0xddf252ad"}},
		addresses: []string{},
		opts: &Options{
			FetchMode: FetchModeLogs,
		},
	}

	ctx := context.Background()
	job := BlockRange{From: 0, To: 9}

	logs, err := processor.fetchWithSplit(ctx, chain, job)
	require.NoError(t, err)
	assert.Len(t, logs, 5, "Should have 5 logs")

	// Verify order: 2, 4, 5, 7, 9
	expectedBlocks := []string{"0x2", "0x4", "0x5", "0x7", "0x9"}
	for i, log := range logs {
		assert.Equal(t, expectedBlocks[i], log.BlockNumber, "Log at index %d should be from block %s", i, expectedBlocks[i])
	}
}

func TestFetchWithSplit_Timeout(t *testing.T) {
	// Create a server that hangs
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  []map[string]any{},
		})
	}))
	defer srv.Close()

	rpcClient := rpc.NewHTTPRPC(srv.URL, 1000, 1000)
	processor := NewProcessor(nil, &NoopSink{})

	chain := &chainState{
		chainInfo: ChainInfo{
			ChainId: "1",
			RPC:     rpcClient,
		},
		topics:    [][]string{{"0xddf252ad"}},
		addresses: []string{},
		opts: &Options{
			FetchMode: FetchModeLogs,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	job := BlockRange{From: 0, To: 7}

	logs, err := processor.fetchWithSplit(ctx, chain, job)
	assert.Error(t, err)
	assert.Nil(t, logs)
	assert.Contains(t, err.Error(), "deadline exceeded")
}
