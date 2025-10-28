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
	chain := ChainInfo{
		ChainId: "592",
		Name: "Astar",
		RPC: rpc,
	}

	processor := NewProcessor()
	processor.AddChain(chain, &opts)
	go func() { _ = processor.Run(ctx)}()


	var logs []Log
	logsCh, err := processor.Logs(chain.ChainId)
	if err != nil {
		log.Fatal(err)
	}
	for {
		select {
		case log, ok := <-logsCh:
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
	chain := ChainInfo{
		ChainId: "592",
		Name: "Astar",
		RPC: rpc,
	}

	processor := NewProcessor()
	processor.AddChain(chain, &opts)
	go func() { _ = processor.Run(ctx)}()



	var logs []Log


	done := make(chan struct{})
	var mu sync.Mutex

	go func() {
		defer close(done) // remove the block when the channel is closed.
		logsCh, err := processor.Logs(chain.ChainId)
	if err != nil {
		log.Fatal(err)
	}
		for {	
			select{
			case <- ctx.Done():
				return
			case l, ok := <- logsCh:
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
	chain := ChainInfo{
		ChainId: "592",
		Name: "Astar",
		RPC: rpc,
	}

	processor := NewProcessor()
	processor.AddChain(chain, &opts)
	go func() { _ = processor.Run(ctx)}()



	var logs []Log


	done := make(chan struct{})
	var mu sync.Mutex

	go func() {
		defer close(done) // remove the block when the channel is closed.
		logsCh, err := processor.Logs(chain.ChainId)
	if err != nil {
		log.Fatal(err)
	}
		for {	
			select{
			case <- ctx.Done():
				return
			case l, ok := <- logsCh:
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
				"result":  "0x1",
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
	chain := ChainInfo{
		ChainId: "592",
		Name: "Astar",
		RPC: rpc,
	}

	processor := NewProcessor()
	processor.AddChain(chain, &opts)
	go func() { _ = processor.Run(ctx)}()

	var logs []Log

	done := make(chan struct{})
	var mu sync.Mutex

	go func() {
		defer close(done) // remove the block when the channel is closed.
		logsCh, err := processor.Logs(chain.ChainId)
	if err != nil {
		log.Fatal(err)
	}
		for {	
			select{
			case <- ctx.Done():
				return
			case l, ok := <- logsCh:
				if !ok {
					return
				}	
				mu.Lock()
				//log.Printf("%v", l)
				logs = append(logs, l)
				if len(logs) >= 1 {
					select {
					case done <- struct{}{}:
					default:
					}
				}
				mu.Unlock()
			}
		}
	}()

	select {
	case <-done:
		// Logs channel closed, processor done
	case <-time.After(2 * time.Second):
		t.Fatal("Test timeout")
	}

	assert.Equal(t, 3, attempts, "Should have retried 3 times")
	assert.Len(t, logs, 1, "Should receive log after retry")
}

func TestMultiChainRun_Success(t *testing.T) {
    // Track calls per chain
    ethCalls := 0
    var mu sync.Mutex
    
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        
        var req struct {
            Method string        `json:"method"`
            Params []interface{} `json:"params"`
        }
        json.NewDecoder(r.Body).Decode(&req)
        
        // Check which chain by port or add chain identifier
        switch req.Method {
        case "eth_blockNumber":
            _ = json.NewEncoder(w).Encode(map[string]any{
                "jsonrpc": "2.0",
                "id":      1,
                "result":  "0x5", // Block 5
            })
            
        case "eth_getLogs":
            mu.Lock()
            ethCalls++ // Count calls
            mu.Unlock()
            
            _ = json.NewEncoder(w).Encode(map[string]any{
                "jsonrpc": "2.0",
                "id":      1,
                "result": []map[string]any{
                    {
                        "Address":          "0xeth",
                        "Topics":           []any{"0xddf252ad"},
                        "Data":             "0x",
                        "BlockNumber":      "0x1",
                        "TransactionHash":  "0xeth_tx",
                        "TransactionIndex": "0x0",
                        "BlockHash":        "0xeth_block",
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
        }
    }))
    defer srv.Close()
    
    // Create two separate RPC clients (simulating different chains)
    ethRPC := NewHTTPRPC(srv.URL, 0)
    polyRPC := NewHTTPRPC(srv.URL, 0)
    
    processor := NewProcessor()
    
    // Add Ethereum chain
    ethOpts := &Options{
        RangeSize:          2,
        FetcherConcurrency: 1,
        StartBlock:         0,
        Confimation:        0,
        LogsBufferSize:     10,
        FetchMode:          FetchModeLogs,
        Topics:             []string{"Transfer(address,address,uint256)"},
    }
    processor.AddChain(ChainInfo{
        ChainId: "1",
        Name:    "Ethereum",
        RPC:     ethRPC,
    }, ethOpts)
    
    // Add Polygon chain
    polyOpts := &Options{
        RangeSize:          2,
        FetcherConcurrency: 1,
        StartBlock:         0,
        Confimation:        0,
        LogsBufferSize:     10,
        FetchMode:          FetchModeLogs,
        Topics:             []string{"Transfer(address,address,uint256)"},
    }
    processor.AddChain(ChainInfo{
        ChainId: "137",
        Name:    "Polygon",
        RPC:     polyRPC,
    }, polyOpts)
    
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    
    go processor.Run(ctx)
    
    // Collect logs from both chains
    ethLogs := []Log{}
    polyLogs := []Log{}
    
    ethCh, err := processor.Logs("1")
    assert.NoError(t, err)
    
    polyCh, err := processor.Logs("137")
    assert.NoError(t, err)
    
    done := make(chan struct{})
    go func() {
        defer close(done)
        timeout := time.After(1 * time.Second)
        for {
            select {
            case log, ok := <-ethCh:
                if !ok {
                    return
                }
                ethLogs = append(ethLogs, log)
            case log, ok := <-polyCh:
                if !ok {
                    return
                }
                polyLogs = append(polyLogs, log)
            case <-timeout:
                return
            case <-ctx.Done():
                return
            }
        }
    }()
    
    // Wait
    select {
    case <-done:
    case <-time.After(2 * time.Second):
    }
    
    cancel()
    time.Sleep(100 * time.Millisecond)
    
    // Verify both chains processed
    assert.GreaterOrEqual(t, len(ethLogs), 1, "Ethereum should have logs")
    assert.GreaterOrEqual(t, len(polyLogs), 1, "Polygon should have logs")
}

func TestMultiChain_IndependentErrors(t *testing.T) {
    // Ethereum server - always fails
    ethCallCount := 0
    ethSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        ethCallCount++
        
        // Always return error for Ethereum
        _ = json.NewEncoder(w).Encode(map[string]any{
            "jsonrpc": "2.0",
            "id":      1,
            "error": map[string]any{
                "code":    -32000,
                "message": "ethereum node is down",
            },
        })
    }))
    defer ethSrv.Close()
    
    // Polygon server - works fine
    polyCallCount := 0
    polySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        polyCallCount++
        
        var req struct {
            Method string        `json:"method"`
            Params []interface{} `json:"params"`
        }
        json.NewDecoder(r.Body).Decode(&req)
        
        switch req.Method {
        case "eth_blockNumber":
            _ = json.NewEncoder(w).Encode(map[string]any{
                "jsonrpc": "2.0",
                "id":      1,
                "result":  "0x2", // Block 2
            })
            
        case "eth_getLogs":
            _ = json.NewEncoder(w).Encode(map[string]any{
                "jsonrpc": "2.0",
                "id":      1,
                "result": []map[string]any{
                    {
                        "Address":          "0xpoly",
                        "Topics":           []any{"0xddf252ad"},
                        "Data":             "0x",
                        "BlockNumber":      "0x1",
                        "TransactionHash":  "0xpoly_tx",
                        "TransactionIndex": "0x0",
                        "BlockHash":        "0xpoly_bh",
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
            http.Error(w, "method not supported", http.StatusBadRequest)
        }
    }))
    defer polySrv.Close()
    
    processor := NewProcessor()
    
    // Fast retry config so Ethereum fails quickly
    fastRetry := &RetryConfig{
        MaxAttempts:    2,
        InitialBackoff: 10 * time.Millisecond,
        MaxBackoff:     20 * time.Millisecond,
        Multiplier:     1.5,
        EnableJitter:   false,
    }
    
    ethOpts := &Options{
        RangeSize:          1,
        FetcherConcurrency: 1,
        StartBlock:         0,
        Confimation:        0,
        LogsBufferSize:     10,
        FetchMode:          FetchModeLogs,
        Topics:             []string{"0xddf252ad"},
        RetryConfig:        fastRetry,
    }
    
    polyOpts := &Options{
        RangeSize:          1,
        FetcherConcurrency: 1,
        StartBlock:         0,
        Confimation:        0,
        LogsBufferSize:     10,
        FetchMode:          FetchModeLogs,
        Topics:             []string{"0xddf252ad"},
        RetryConfig:        fastRetry,
    }
    
    // Add both chains
    err := processor.AddChain(ChainInfo{
        ChainId: "1",
        Name:    "Ethereum",
        RPC:     NewHTTPRPC(ethSrv.URL, 0),
    }, ethOpts)
    assert.NoError(t, err)
    
    err = processor.AddChain(ChainInfo{
        ChainId: "137",
        Name:    "Polygon",
        RPC:     NewHTTPRPC(polySrv.URL, 0),
    }, polyOpts)
    assert.NoError(t, err)
    
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    
    // Collect Polygon logs
    polyCh, err := processor.Logs("137")
    assert.NoError(t, err)
    
    polyLogs := []Log{}
    var logsMu sync.Mutex
    
    go func() {
        for log := range polyCh {
            logsMu.Lock()
            polyLogs = append(polyLogs, log)
            logsMu.Unlock()
        }
    }()
    
    // Run processor
    runErr := processor.Run(ctx)
    
    // Wait a bit for log collection
    time.Sleep(100 * time.Millisecond)
    
    // Assertions
    t.Logf("Run error: %v", runErr)
    t.Logf("Ethereum calls: %d", ethCallCount)
    t.Logf("Polygon calls: %d", polyCallCount)
    
    logsMu.Lock()
    polyLogCount := len(polyLogs)
    logsMu.Unlock()
    t.Logf("Polygon logs collected: %d", polyLogCount)
    
    assert.Greater(t, ethCallCount, 0, "Ethereum should have attempted calls")
    assert.Greater(t, polyCallCount, 0, "Polygon should have made calls")
    assert.Error(t, runErr, "Should get error from Ethereum chain")
}

func TestMultiChain_BothChainsSucceed(t *testing.T) {
    // Both servers work fine
    createWorkingServer := func(chainName string) *httptest.Server {
        return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("Content-Type", "application/json")
            
            var req struct {
                Method string        `json:"method"`
                Params []interface{} `json:"params"`
            }
            json.NewDecoder(r.Body).Decode(&req)
            
            switch req.Method {
            case "eth_blockNumber":
                _ = json.NewEncoder(w).Encode(map[string]any{
                    "jsonrpc": "2.0",
                    "id":      1,
                    "result":  "0x2",
                })
                
            case "eth_getLogs":
                _ = json.NewEncoder(w).Encode(map[string]any{
                    "jsonrpc": "2.0",
                    "id":      1,
                    "result": []map[string]any{
                        {
                            "Address":          fmt.Sprintf("0x%s", chainName),
                            "Topics":           []any{"0xddf252ad"},
                            "Data":             "0x",
                            "BlockNumber":      "0x1",
                            "TransactionHash":  fmt.Sprintf("0x%s_tx", chainName),
                            "TransactionIndex": "0x0",
                            "BlockHash":        fmt.Sprintf("0x%s_bh", chainName),
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
            }
        }))
    }
    
    ethSrv := createWorkingServer("eth")
    defer ethSrv.Close()
    
    polySrv := createWorkingServer("poly")
    defer polySrv.Close()
    
    processor := NewProcessor()
    
    opts := &Options{
        RangeSize:          1,
        FetcherConcurrency: 1,
        StartBlock:         0,
        Confimation:        0,
        LogsBufferSize:     10,
        FetchMode:          FetchModeLogs,
        Topics:             []string{"0xddf252ad"},
        RetryConfig: &RetryConfig{
            MaxAttempts:    3,
            InitialBackoff: 10 * time.Millisecond,
            MaxBackoff:     50 * time.Millisecond,
        },
    }
    
    processor.AddChain(ChainInfo{ChainId: "1", Name: "Eth", RPC: NewHTTPRPC(ethSrv.URL, 0)}, opts)
    processor.AddChain(ChainInfo{ChainId: "137", Name: "Poly", RPC: NewHTTPRPC(polySrv.URL, 0)}, opts)
    
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    
    // Collect logs from both chains
    ethCh, _ := processor.Logs("1")
    polyCh, _ := processor.Logs("137")
    
    ethLogs := []Log{}
    polyLogs := []Log{}
    var mu sync.Mutex
    
    go func() {
        for log := range ethCh {
            mu.Lock()
            ethLogs = append(ethLogs, log)
            mu.Unlock()
        }
    }()
    
    go func() {
        for log := range polyCh {
            mu.Lock()
            polyLogs = append(polyLogs, log)
            mu.Unlock()
        }
    }()
    
    go processor.Run(ctx)
    
    // Wait for processing
    time.Sleep(500 * time.Millisecond)
    cancel()
    time.Sleep(100 * time.Millisecond)
    
    // Both chains should have logs
    mu.Lock()
    assert.GreaterOrEqual(t, len(ethLogs), 1, "Ethereum should have logs")
    assert.GreaterOrEqual(t, len(polyLogs), 1, "Polygon should have logs")
    
    // Verify logs are from correct chains
    if len(ethLogs) > 0 {
        assert.Contains(t, ethLogs[0].Address, "eth")
    }
    if len(polyLogs) > 0 {
        assert.Contains(t, polyLogs[0].Address, "poly")
    }
    mu.Unlock()
}
func TestMultiChain_AddChainWhileRunning(t *testing.T) {
    processor := NewProcessor()
    
    opts := &Options{
        RangeSize:  2,
        StartBlock: 0,
    }
    
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        time.Sleep(2 * time.Second) // Make it run a while
    }))
    defer srv.Close()
    
    processor.AddChain(ChainInfo{ChainId: "1", RPC: NewHTTPRPC(srv.URL, 0)}, opts)
    
    ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
    defer cancel()
    
    go processor.Run(ctx)
    time.Sleep(100 * time.Millisecond) // Let it start
    
    // Try to add chain while running
    err := processor.AddChain(ChainInfo{ChainId: "137", RPC: NewHTTPRPC(srv.URL, 0)}, opts)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "running")
}

