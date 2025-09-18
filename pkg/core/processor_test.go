package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"

	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRunWithOneLog_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req struct {
			Method string        `json:"method"`
			Params []interface{} `json:"params"`
			ID     interface{}   `json:"id"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "eth_blockNumber":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result":  "0x10d4f",
			})

		case "eth_getLogs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": []map[string]any{
					{
						"Address":          "0xabc",
						"Topics": []any{"0xddf252ad"},
						"Data":             "0x",
						"BlockNumber":      "0x1",
						"TransactionHash":  "0xth1",
						"TransactionIndex": "0",
						"BlockHash":        "0xbh1",
						"LogIndex":         "0x0",
						"Removed":          false,
					},
				},
			})

		default:
			http.Error(w, "method no supported", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	opts := Options{
		RangeSize:          50,
		BatchSize:          50,
		DecoderConcurrency: 1,
		FetcherConcurrency: 1,
		StartBlock:         0,
		Confimation:        0,
		LogsBufferSize:     1024,
	}
	processor := NewProcessor(rpc, &opts)
	go func() { _ = processor.Run(ctx)}()



	log := <- processor.Logs()

	cancel()

	assert.Equal(t, log.Address, "0xabc")
	assert.Equal(t, log.Topics, []any{"0xddf252ad"})
	assert.Equal(t, log.Data, "0x")
	assert.Equal(t, log.BlockNumber, "0x1")
	assert.Equal(t, log.TransactionHash, "0xth1")
	assert.Equal(t, log.TransactionIndex, "0")
	assert.Equal(t, log.LogIndex, "0x0")
	assert.Equal(t, log.Removed, false)
}

func TestRunWithMultipleLog_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req struct {
			Method string        `json:"method"`
			Params []interface{} `json:"params"`
			ID     interface{}   `json:"id"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "eth_blockNumber":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result":  "0x10d4f",
			})

		case "eth_getLogs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": []map[string]any{
					{
						"Address":          "0xabc",
						"Topics": []any{"0xddf252ad"},
						"Data":             "0x",
						"BlockNumber":      "0x1",
						"TransactionHash":  "0xth1",
						"TransactionIndex": "0",
						"BlockHash":        "0xbh1",
						"LogIndex":         "0x0",
						"Removed":          false,
					},
					{
						"Address":          "0xabcd",
						"Topics": []any{"0xddf252ad"},
						"Data":             "0x",
						"BlockNumber":      "0x1",
						"TransactionHash":  "0xth1",
						"TransactionIndex": "0",
						"BlockHash":        "0xbh1",
						"LogIndex":         "0x0",
						"Removed":          false,
					},
					{
						"Address":          "0xabcde",
						"Topics": []any{"0xddf252ad"},
						"Data":             "0x",
						"BlockNumber":      "0x1",
						"TransactionHash":  "0xth1",
						"TransactionIndex": "0",
						"BlockHash":        "0xbh1",
						"LogIndex":         "0x0",
						"Removed":          false,
					},
					{
						"Address":          "0xabcdef",
						"Topics": []any{"0xddf252ad"},
						"Data":             "0x",
						"BlockNumber":      "0x1",
						"TransactionHash":  "0xth1",
						"TransactionIndex": "0",
						"BlockHash":        "0xbh1",
						"LogIndex":         "0x0",
						"Removed":          false,
					},
					{
						"Address":          "0xabcdefg",
						"Topics": []any{"0xddf252ad"},
						"Data":             "0x",
						"BlockNumber":      "0x1",
						"TransactionHash":  "0xth1",
						"TransactionIndex": "0",
						"BlockHash":        "0xbh1",
						"LogIndex":         "0x0",
						"Removed":          false,
					},
				},
			})

		default:
			http.Error(w, "method no supported", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	opts := Options{
		RangeSize:          50,
		BatchSize:          50,
		DecoderConcurrency: 2,
		FetcherConcurrency: 4,
		StartBlock:         0,
		Confimation:        0,
		LogsBufferSize:     1024,
	}
	processor := NewProcessor(rpc, &opts)
	go func() { _ = processor.Run(ctx)}()



	var logs []Log


	done := make(chan struct{})
	var mu sync.Mutex

	go func() {
		defer close(done) // remove the block when the channel is closed.
		for {	
			select{
			case <- ctx.Done():
				return
			case l, ok := <- processor.Logs():
				if !ok {
					return
				}	
				mu.Lock()
				logs = append(logs, l)
				mu.Unlock()
			}
		}
	}()

	<- done // blocks the the test

	assert.Equal(t, len(logs), 6895)
	assert.Equal(t, logs[0].Address, "0xabc")
	assert.Equal(t, logs[1].Address, "0xabcd")
	assert.Equal(t, logs[2].Address, "0xabcde")
	assert.Equal(t, logs[3].Address, "0xabcdef")
	assert.Equal(t, logs[4].Address, "0xabcdefg")
	assert.Equal(t, logs[5].Address, "0xabc")
}
