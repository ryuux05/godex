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
				"Number":     uint64(12345),
				"Hash":       "0xabc",
				"ParentHash": "0xdef",
				"Timestamp":  uint64(1700000000),
			},
		})
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := rpc.GetBlock(ctx, "0x3039") // "0x3039" == 12345
	assert.NoError(t, err)
	assert.Equal(t, uint64(12345), got.Number)
	assert.Equal(t, "0xabc", got.Hash)
	assert.Equal(t, "0xdef", got.ParentHash)
	assert.Equal(t, uint64(1700000000), got.Timestamp)
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
		Topics:    []any{"0xddf252ad"},
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