package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

	rpc := NewHTTPRPC(srv.URL, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := rpc.Head(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "0x10d4f", got)
}

func TestHead_RPCError (t *testing.T) {
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

	rpc := NewHTTPRPC(srv.URL, 0)
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

	rpc := NewHTTPRPC(srv.URL, 0)
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

	rpc := NewHTTPRPC(srv.URL, 0)
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

	rpc := NewHTTPRPC(srv.URL, 0)
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

	rpc := NewHTTPRPC(srv.URL, 0)
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

	rpc := NewHTTPRPC(srv.URL, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	filter := Filter{
		FromBlock: "0x1",
		ToBlock:   "0x2",
		Address:   []string{"0xabc"},
		Topics:    []string{"0xddf252ad"},
	}
	logs, err := rpc.GetLogs(ctx, filter)
	assert.NoError(t, err)
	assert.Len(t, logs, 1)
	assert.Equal(t, "0xabc", logs[0].Address)
	assert.Equal(t,[]any{"0xddf252ad"}, logs[0].Topics)
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

	rpc := NewHTTPRPC(srv.URL, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := rpc.GetLogs(ctx, Filter{})
	assert.Error(t, err)
}

func TestGetLogs_HTTPStatusNotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := rpc.GetLogs(ctx, Filter{})
	assert.Error(t, err)
}

func TestGetBlockReceipts_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any {
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

	rpc := NewHTTPRPC(srv.URL, 0)
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
	
	rpc := NewHTTPRPC(srv.URL, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	receipts, err := rpc.GetBlockReceipts(ctx, "0x000")
	assert.Error(t, err)
	assert.Len(t, receipts, 0)
}

func TestGetBlockReceipts_EmptyBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any {
			"jsonrpc": "2.0",
			"id":      1,
			"result": []map[string]any {},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	receipt, err := rpc.GetBlockReceipts(ctx, "0x000")
	assert.NoError(t, err)
	assert.Len(t, receipt, 0)
}