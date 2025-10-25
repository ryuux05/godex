package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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

		case "eth_getBlockByNumber":
			s := fmt.Sprintf("%s", req.Params[0])
			blockNum, err := HexQtyToUint64(s)
			assert.NoError(t, err)


			_ = json.NewEncoder(w).Encode(map[string]any {
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any {
					"Number": req.Params[0],
					"Hash": req.Params[0],
					"ParentHash": Uint64ToHexQty(blockNum - 1), 
					"Timestamp": fmt.Sprintf("%d",time.Now().Unix()),
				},
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

		case "eth_getBlockReceipts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": []map[string]any{
					// Receipt 1: Transaction with Transfer event log
					{
						"BlockHash":         "0xbh1",
						"BlockNumber":       "0x1",
						"ContractAddress":   nil,
						"CumulativeGasUsed": "0x5208",
						"EffectiveGasPrice": "0x3b9aca00",
						"From":              "0xsender",
						"GasUsed":           "0x5208",
						"Logs": []map[string]any{
							{
								"Address":          "0xabc",
								"Topics":           []any{"0xddf252ad"},
								"Data":             "0x",
								"BlockNumber":      "0x1",
								"TransactionHash":  "0xth1",
								"TransactionIndex": "0x0",
								"BlockHash":        "0xbh1",
								"LogIndex":         "0x0",
								"Removed":          false,
							},
						},
						"LogsBloom":        "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
						"Status":           "0x1",
						"To":               "0xabc",
						"TransactionHash":  "0xth1",
						"TransactionIndex": "0x0",
						"Type":             "0x2",
					},
					// Receipt 2: Transaction with no logs
					{
						"BlockHash":         "0xbh1",
						"BlockNumber":       "0x1",
						"ContractAddress":   nil,
						"CumulativeGasUsed": "0xa410",
						"EffectiveGasPrice": "0x3b9aca00",
						"From":              "0xsender2",
						"GasUsed":           "0x5208",
						"Logs":              []map[string]any{},
						"LogsBloom":         "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
						"Status":            "0x1",
						"To":                "0xreceiver",
						"TransactionHash":   "0xth2",
						"TransactionIndex":  "0x1",
						"Type":              "0x2",
					},
				},
			})

		default:
			http.Error(w, "method no supported", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := Options{
		RangeSize:          100,
		BatchSize:          50,
		DecoderConcurrency: 1,
		FetcherConcurrency: 4,
		StartBlock:         0,
		Confimation:        0,
		LogsBufferSize:     1024,
		FetchMode: FetchModeReceipts,
	}
	processor := NewProcessor(rpc, &opts)
	go func() { _ = processor.Run(ctx)}()


	var logs []Log
	for {
		select {
		case log, ok := <-processor.Logs():
			if !ok {
				goto done // Channel closed
			}
			logs = append(logs, log)
		case <-ctx.Done():
			goto done // Timeout or cancellation
		}
	}
	done:
	fmt.Printf("Collected %d logs\n", len(logs))


	cancel()


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
				"result":  "0x3e8",
			})

		case "eth_getBlockByNumber":
			s := fmt.Sprintf("%s", req.Params[0])
			blockNum, err := HexQtyToUint64(s)
			assert.NoError(t, err)


			_ = json.NewEncoder(w).Encode(map[string]any {
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any {
					"Number": req.Params[0],
					"Hash": req.Params[0],
					"ParentHash": Uint64ToHexQty(blockNum - 1), 
					"Timestamp": fmt.Sprintf("%d",time.Now().Unix()),
				},
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
				//log.Printf("%v", l)
				logs = append(logs, l)
				mu.Unlock()
			}
		}
	}()

	<- done // blocks the the test
	log.Println(len(logs))

	assert.Equal(t, len(logs), 100)
	assert.Equal(t, logs[0].Address, "0xabc")
	assert.Equal(t, logs[1].Address, "0xabcd")
	assert.Equal(t, logs[2].Address, "0xabcde")
	assert.Equal(t, logs[3].Address, "0xabcdef")
	assert.Equal(t, logs[4].Address, "0xabcdefg")
	assert.Equal(t, logs[5].Address, "0xabc")
}

func TestReorg_Success(t *testing.T) {
	flip := false
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
				"result":  "0x64",
			})

		case "eth_getBlockByNumber":
			s := fmt.Sprintf("%s", req.Params[0])

			blockNum, err := HexQtyToUint64(s)
			assert.NoError(t, err)

			if !flip && blockNum == 41 {
				flip = true
				_ = json.NewEncoder(w).Encode(map[string]any {
					"jsonrpc": "2.0",
					"id":      1,
					"result": map[string]any {
						"Number": req.Params[0],
						"Hash": req.Params[0],
						"ParentHash": "somerandomshit", 
						"Timestamp": fmt.Sprintf("%d",time.Now().Unix()),
					},
				})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any {
					"jsonrpc": "2.0",
					"id":      1,
					"result": map[string]any {
						"Number": req.Params[0],
						"Hash": req.Params[0],
						"ParentHash": Uint64ToHexQty(blockNum - 1), 
						"Timestamp": fmt.Sprintf("%d",time.Now().Unix()),
					},
				})
			}

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
		RangeSize:          10,
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
				//log.Printf("%v", l)
				logs = append(logs, l)
				mu.Unlock()
			}
		}
	}()

	<- done // blocks the the test
	log.Println(len(logs))

	assert.Equal(t, len(logs), 10)
}

func TestRunWithRetry_Success(t *testing.T) {
	attempts := 0
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
				"result":  "0xa",
			})

		case "eth_getLogs":
			attempts++
			if attempts < 3 {
				// First 2 attempts: return 503 error
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"error": map[string]any{
						"code":    -32000,
						"message": "oops",
					},
				})
				return
			}
			// 3rd attempt: succeed
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": []map[string]any{
					{
						"Address":          "0xabc",
						"Topics":           []any{"0xddf252ad"},
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

		case "eth_getBlockByNumber":
			blockNum, _ := HexQtyToUint64(fmt.Sprintf("%s", req.Params[0]))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"Number":     req.Params[0],
					"Hash":       req.Params[0],
					"ParentHash": Uint64ToHexQty(blockNum - 1),
					"Timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
				},
			})

		default:
			http.Error(w, "method no supported", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	rpc := NewHTTPRPC(srv.URL, 0)

	ctx, cancel := context.WithTimeout(context.Background(),  5 * time.Second)
	defer cancel()

	retryConfig := RetryConfig{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
		EnableJitter:   true,
	}

	opts := Options{
		RangeSize:          1,
		BatchSize:          50,
		DecoderConcurrency: 1,
		FetcherConcurrency: 1,
		StartBlock:         0,
		Confimation:        0,
		LogsBufferSize:     1024,
		FetchMode: FetchModeLogs,
		RetryConfig: &retryConfig,
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
				//log.Printf("%v", l)
				logs = append(logs, l)
				mu.Unlock()
				done <- struct{}{}
			}
		}
	}()

	select {
	case <-done:
		// Logs channel closed, processor done
	case <-time.After(2 * time.Second):
		t.Fatal("Test timeout")
	}

	assert.Equal(t, 4, attempts, "Should have retried 3 times")
	assert.Len(t, logs, 1, "Should receive log after retry")
}

