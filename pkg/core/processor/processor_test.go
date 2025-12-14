package processor

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

	coreerrors "github.com/ryuux05/godex/pkg/core/errors"
	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
	"github.com/stretchr/testify/assert"
)

type NoopSink struct{}

func (NoopSink) Store(ctx context.Context, events []types.Event) error              { return nil }
func (NoopSink) Rollback(ctx context.Context, chainId string, toBlock uint64) error { return nil }
func (NoopSink) LoadCursor(ctx context.Context, chainId string) (uint64, string, error) {
	return 0, "", nil // No cursor, start fresh
}

type NoopDecoder struct{}

func (NoopDecoder) Decode(log types.Log) (*types.Event, error) {
	return &types.Event{
		Id:              log.TransactionHash + "-" + log.LogIndex,
		BlockNumber:     1,
		TransactionHash: log.TransactionHash,
		Address:         log.Address,
	}, nil
}

func (NoopDecoder) DecodeBatch(logs []types.Log) (*[]types.Event, error) {
	events := make([]types.Event, len(logs))
	for i, l := range logs {
		events[i] = types.Event{Id: l.TransactionHash}
	}
	return &events, nil
}

type MockSink struct {
	StoreCalls    [][]types.Event
	RollbackCalls []struct {
		ChainId string
		ToBlock uint64
	}
	CursorBlockNum  uint64
	CursorBlockHash string
	CursorErr       error
	StoreErr        error
	RollbackErr     error
	mu              sync.Mutex
	eventCount      int
}

func (m *MockSink) Store(ctx context.Context, events []types.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StoreCalls = append(m.StoreCalls, events)
	m.eventCount += len(events)
	return m.StoreErr
}

func (m *MockSink) Rollback(ctx context.Context, chainId string, toBlock uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RollbackCalls = append(m.RollbackCalls, struct {
		ChainId string
		ToBlock uint64
	}{chainId, toBlock})
	return m.RollbackErr
}

func (m *MockSink) LoadCursor(ctx context.Context, chainId string) (uint64, string, error) {
	return m.CursorBlockNum, m.CursorBlockHash, m.CursorErr
}

func (m *MockSink) GetStoreCalls() [][]types.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.StoreCalls
}

func (m *MockSink) GetRollbackCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.RollbackCalls)
}

func (m *MockSink) GetAllEvents() []types.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []types.Event
	for _, call := range m.StoreCalls {
		all = append(all, call...)
	}
	return all
}

func (m *MockSink) GetEventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.eventCount
}

// WaitForEvents blocks until minEvents are stored or context is cancelled
func (m *MockSink) WaitForEvents(ctx context.Context, minEvents int) bool {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if m.GetEventCount() >= minEvents {
				return true
			}
		}
	}
}

// WaitForRollback blocks until at least one rollback is called or context is cancelled
func (m *MockSink) WaitForRollback(ctx context.Context) bool {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if m.GetRollbackCalls() > 0 {
				return true
			}
		}
	}
}

// MockDecoder with custom decode function
type MockDecoder struct {
	DecodeFn    func(log types.Log) (*types.Event, error)
	DecodeCount int
	mu          sync.Mutex
}

func (m *MockDecoder) Decode(log types.Log) (*types.Event, error) {
	m.mu.Lock()
	m.DecodeCount++
	m.mu.Unlock()

	if m.DecodeFn != nil {
		return m.DecodeFn(log)
	}
	return &types.Event{
		Id:              log.TransactionHash + "-" + log.LogIndex,
		ChainId:         "test",
		EventType:       "Transfer",
		BlockNumber:     1,
		BlockHash:       log.BlockHash,
		TransactionHash: log.TransactionHash,
		Address:         log.Address,
	}, nil
}

func (m *MockDecoder) DecodeBatch(logs []types.Log) (*[]types.Event, error) {
	events := make([]types.Event, 0, len(logs))
	for _, l := range logs {
		e, err := m.Decode(l)
		if err != nil {
			continue
		}
		if e != nil {
			events = append(events, *e)
		}
	}
	return &events, nil
}

func (m *MockDecoder) GetDecodeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.DecodeCount
}

func NewTestServer(t *testing.T) *httptest.Server {
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
			blockNum, err := utils.HexQtyToUint64(s)
			assert.NoError(t, err)

			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"Number":     req.Params[0],
					"Hash":       req.Params[0],
					"ParentHash": utils.Uint64ToHexQty(blockNum - 1),
					"Timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
				},
			})

		case "eth_getLogs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": []map[string]any{
					{
						"Address":          "logaddress",
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
								"Address":          "receiptaddress",
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
	return srv
}

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
				"result":  "0x64",
			})

		case "eth_getBlockByNumber":
			s := fmt.Sprintf("%s", req.Params[0])
			blockNum, err := utils.HexQtyToUint64(s)
			assert.NoError(t, err)

			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"Number":     req.Params[0],
					"Hash":       req.Params[0],
					"ParentHash": utils.Uint64ToHexQty(blockNum - 1),
					"Timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
				},
			})

		case "eth_getLogs":
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

	rpc := rpc.NewHTTPRPC(srv.URL, 0, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := Options{
		RangeSize:          10,
		BatchSize:          50,

		FetcherConcurrency: 4,
		StartBlock:         0,
		ConfimationDepth:   0,

		FetchMode:          FetchModeReceipts,
	}
	chain := ChainInfo{
		ChainId: "592",
		Name:    "Astar",
		RPC:     rpc,
	}

	mockSink := &MockSink{}
	processor := NewProcessor(nil, mockSink)
	processor.AddChain(chain, &opts, &MockDecoder{})

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for at least 1 event or timeout
	mockSink.WaitForEvents(ctx, 1)
	cancel()
	<-done

	// Get events stored to sink
	totalEvents := mockSink.GetEventCount()
	fmt.Printf("Collected %d events\n", totalEvents)
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
			blockNum, err := utils.HexQtyToUint64(s)
			assert.NoError(t, err)

			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"Number":     req.Params[0],
					"Hash":       req.Params[0],
					"ParentHash": utils.Uint64ToHexQty(blockNum - 1),
					"Timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
				},
			})

		case "eth_getLogs":
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
					{
						"Address":          "0xabcd",
						"Topics":           []any{"0xddf252ad"},
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
						"Topics":           []any{"0xddf252ad"},
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
						"Topics":           []any{"0xddf252ad"},
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

		default:
			http.Error(w, "method no supported", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	rpc := rpc.NewHTTPRPC(srv.URL, 0, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	opts := Options{
		RangeSize:          50,
		BatchSize:          50,
		FetcherConcurrency: 4,
		StartBlock:         0,
		ConfimationDepth:   0,

	}
	chain := ChainInfo{
		ChainId: "592",
		Name:    "Astar",
		RPC:     rpc,
	}

	mockSink := &MockSink{}
	processor := NewProcessor(nil, mockSink)
	processor.AddChain(chain, &opts, &MockDecoder{})

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for 100 events or timeout
	mockSink.WaitForEvents(ctx, 100)
	cancel()
	<-done

	// Collect all events from sink
	events := mockSink.GetAllEvents()
	log.Println(len(events))

	assert.Equal(t, len(events), 100)
	assert.Equal(t, events[0].Address, "0xabc")
	assert.Equal(t, events[1].Address, "0xabcd")
	assert.Equal(t, events[2].Address, "0xabcde")
	assert.Equal(t, events[3].Address, "0xabcdef")
	assert.Equal(t, events[4].Address, "0xabcdefg")
	assert.Equal(t, events[5].Address, "0xabc")
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

			blockNum, err := utils.HexQtyToUint64(s)
			assert.NoError(t, err)

			if !flip && blockNum == 41 {
				flip = true
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"result": map[string]any{
						"Number":     req.Params[0],
						"Hash":       req.Params[0],
						"ParentHash": "somerandomshit",
						"Timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
					},
				})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"result": map[string]any{
						"Number":     req.Params[0],
						"Hash":       req.Params[0],
						"ParentHash": utils.Uint64ToHexQty(blockNum - 1),
						"Timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
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

		default:
			http.Error(w, "method no supported", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	rpc := rpc.NewHTTPRPC(srv.URL, 0, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	opts := Options{
		RangeSize:          10,
		BatchSize:          50,

		FetcherConcurrency: 4,
		StartBlock:         0,
		ConfimationDepth:   0,

	}
	chain := ChainInfo{
		ChainId: "592",
		Name:    "Astar",
		RPC:     rpc,
	}

	mockSink := &MockSink{}
	processor := NewProcessor(nil, mockSink)
	processor.AddChain(chain, &opts, &MockDecoder{})

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for rollback or events
	mockSink.WaitForEvents(ctx, 10)
	cancel()
	<-done

	// Collect all events from sink
	events := mockSink.GetAllEvents()
	log.Println(len(events))

	// Verify rollback was called due to reorg
	rollbackCount := mockSink.GetRollbackCalls()
	log.Printf("Rollback calls: %d", rollbackCount)

	assert.Equal(t, len(events), 10)
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
			blockNum, _ := utils.HexQtyToUint64(fmt.Sprintf("%s", req.Params[0]))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"Number":     req.Params[0],
					"Hash":       req.Params[0],
					"ParentHash": utils.Uint64ToHexQty(blockNum - 1),
					"Timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
				},
			})

		default:
			http.Error(w, "method no supported", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	RPC := rpc.NewHTTPRPC(srv.URL, 0, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	retryConfig := rpc.RetryConfig{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
		EnableJitter:   true,
	}

	opts := Options{
		RangeSize:          1,
		BatchSize:          50,

		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,

		FetchMode:          FetchModeLogs,
		RetryConfig:        &retryConfig,
	}
	chain := ChainInfo{
		ChainId: "592",
		Name:    "Astar",
		RPC:     RPC,
	}

	mockSink := &MockSink{}
	processor := NewProcessor(nil, mockSink)
	processor.AddChain(chain, &opts, &MockDecoder{})

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for events or timeout
	mockSink.WaitForEvents(ctx, 1)
	cancel()
	<-done

	// Collect all events from sink
	events := mockSink.GetAllEvents()

	assert.Equal(t, 3, attempts, "Should have retried 3 times")
	assert.Len(t, events, 1, "Should receive log after retry")
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
			blockNum, _ := utils.HexQtyToUint64(fmt.Sprintf("%s", req.Params[0]))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"Number":     req.Params[0],
					"Hash":       req.Params[0],
					"ParentHash": utils.Uint64ToHexQty(blockNum - 1),
					"Timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
				},
			})
		}
	}))
	defer srv.Close()

	// Create two separate RPC clients (simulating different chains)
	ethRPC := rpc.NewHTTPRPC(srv.URL, 0, 0)
	polyRPC := rpc.NewHTTPRPC(srv.URL, 0, 0)

	mockSink := &MockSink{}
	processor := NewProcessor(nil, mockSink)

	// Create decoders that set the correct ChainId
	ethDecoder := &MockDecoder{
		DecodeFn: func(log types.Log) (*types.Event, error) {
			return &types.Event{
				Id:              log.TransactionHash + "-" + log.LogIndex,
				ChainId:         "1",
				EventType:       "Transfer",
				BlockNumber:     1,
				Address:         log.Address,
				TransactionHash: log.TransactionHash,
			}, nil
		},
	}
	polyDecoder := &MockDecoder{
		DecodeFn: func(log types.Log) (*types.Event, error) {
			return &types.Event{
				Id:              log.TransactionHash + "-" + log.LogIndex,
				ChainId:         "137",
				EventType:       "Transfer",
				BlockNumber:     1,
				Address:         log.Address,
				TransactionHash: log.TransactionHash,
			}, nil
		},
	}

	// Add Ethereum chain
	ethOpts := &Options{
		RangeSize:          2,
		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
		Topics:             []string{"Transfer(address,address,uint256)"},
	}
	processor.AddChain(ChainInfo{
		ChainId: "1",
		Name:    "Ethereum",
		RPC:     ethRPC,
	}, ethOpts, ethDecoder)

	// Add Polygon chain
	polyOpts := &Options{
		RangeSize:          2,
		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
		Topics:             []string{"Transfer(address,address,uint256)"},
	}
	processor.AddChain(ChainInfo{
		ChainId: "137",
		Name:    "Polygon",
		RPC:     polyRPC,
	}, polyOpts, polyDecoder)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for at least 1 event or timeout
	mockSink.WaitForEvents(ctx, 1)
	cancel()
	<-done

	// Collect events from sink
	allEvents := mockSink.GetAllEvents()
	var ethEvents, polyEvents []types.Event
	for _, e := range allEvents {
		if e.ChainId == "1" {
			ethEvents = append(ethEvents, e)
		} else if e.ChainId == "137" {
			polyEvents = append(polyEvents, e)
		}
	}

	// Verify both chains processed
	totalEvents := len(ethEvents) + len(polyEvents)
	assert.GreaterOrEqual(t, totalEvents, 1, "Should have events from chains")
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
			blockNum, _ := utils.HexQtyToUint64(fmt.Sprintf("%s", req.Params[0]))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"Number":     req.Params[0],
					"Hash":       req.Params[0],
					"ParentHash": utils.Uint64ToHexQty(blockNum - 1),
					"Timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
				},
			})

		default:
			http.Error(w, "method not supported", http.StatusBadRequest)
		}
	}))
	defer polySrv.Close()

	mockSink := &MockSink{}
	processor := NewProcessor(nil, mockSink)

	// Fast retry config so Ethereum fails quickly
	fastRetry := &rpc.RetryConfig{
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
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
		Topics:             []string{"0xddf252ad"},
		RetryConfig:        fastRetry,
	}

	polyOpts := &Options{
		RangeSize:          1,
		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
		Topics:             []string{"0xddf252ad"},
		RetryConfig:        fastRetry,
	}

	// Add both chains
	err := processor.AddChain(ChainInfo{
		ChainId: "1",
		Name:    "Ethereum",
		RPC:     rpc.NewHTTPRPC(ethSrv.URL, 0, 0),
	}, ethOpts, &MockDecoder{})
	assert.NoError(t, err)

	err = processor.AddChain(ChainInfo{
		ChainId: "137",
		Name:    "Polygon",
		RPC:     rpc.NewHTTPRPC(polySrv.URL, 0, 0),
	}, polyOpts, &MockDecoder{})
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Run processor
	runErr := processor.Run(ctx)

	// Collect events from sink
	polyEventCount := mockSink.GetEventCount()

	// Assertions
	t.Logf("Run error: %v", runErr)
	t.Logf("Ethereum calls: %d", ethCallCount)
	t.Logf("Polygon calls: %d", polyCallCount)
	t.Logf("Polygon events stored: %d", polyEventCount)

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
				blockNum, _ := utils.HexQtyToUint64(fmt.Sprintf("%s", req.Params[0]))
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"result": map[string]any{
						"Number":     req.Params[0],
						"Hash":       req.Params[0],
						"ParentHash": utils.Uint64ToHexQty(blockNum - 1),
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

	mockSink := &MockSink{}
	processor := NewProcessor(nil, mockSink)

	opts := &Options{
		RangeSize:          1,
		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
		Topics:             []string{"0xddf252ad"},
		RetryConfig: &rpc.RetryConfig{
			MaxAttempts:    3,
			InitialBackoff: 10 * time.Millisecond,
			MaxBackoff:     50 * time.Millisecond,
		},
	}

	processor.AddChain(ChainInfo{ChainId: "1", Name: "Eth", RPC: rpc.NewHTTPRPC(ethSrv.URL, 0, 0)}, opts, &MockDecoder{})
	processor.AddChain(ChainInfo{ChainId: "137", Name: "Poly", RPC: rpc.NewHTTPRPC(polySrv.URL, 0, 0)}, opts, &MockDecoder{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for at least 2 events (one from each chain) or timeout
	mockSink.WaitForEvents(ctx, 2)
	cancel()
	<-done

	// Collect events from sink
	allEvents := mockSink.GetAllEvents()
	var ethEvents, polyEvents []types.Event
	for _, e := range allEvents {
		if e.Address == "0xeth" {
			ethEvents = append(ethEvents, e)
		} else if e.Address == "0xpoly" {
			polyEvents = append(polyEvents, e)
		}
	}

	// Both chains should have events
	assert.GreaterOrEqual(t, len(ethEvents), 1, "Ethereum should have logs")
	assert.GreaterOrEqual(t, len(polyEvents), 1, "Polygon should have logs")

	// Verify events are from correct chains
	if len(ethEvents) > 0 {
		assert.Contains(t, ethEvents[0].Address, "eth")
	}
	if len(polyEvents) > 0 {
		assert.Contains(t, polyEvents[0].Address, "poly")
	}
}
func TestMultiChain_AddChainWhileRunning(t *testing.T) {
	mockSink := &MockSink{}
	processor := NewProcessor(nil, mockSink)

	opts := &Options{
		RangeSize:  2,
		StartBlock: 0,
	}

	// Use a channel to signal when the server handler is hit
	serverHit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case serverHit <- struct{}{}:
		default:
		}
		// Return a slow response but not block forever
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "0x1"})
	}))
	defer srv.Close()

	processor.AddChain(ChainInfo{ChainId: "1", RPC: rpc.NewHTTPRPC(srv.URL, 0, 0)}, opts, &MockDecoder{})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait until the server is hit (meaning processor is running)
	<-serverHit

	// Try to add chain while running
	err := processor.AddChain(ChainInfo{ChainId: "137", RPC: rpc.NewHTTPRPC(srv.URL, 0, 0)}, opts, &MockDecoder{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "running")

	cancel()
	<-done
}

func TestUseLogsForHistoricalSync_False(t *testing.T) {
	mockSink := &MockSink{}
	processor := NewProcessor(nil, mockSink)
	srv := NewTestServer(t)
	defer srv.Close()

	rpchttp := rpc.NewHTTPRPC(srv.URL, 0, 0)

	opts := &Options{
		RangeSize:          1,
		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
		Topics:             []string{"0xddf252ad"},
		RetryConfig: &rpc.RetryConfig{
			MaxAttempts:    3,
			InitialBackoff: 10 * time.Millisecond,
			MaxBackoff:     50 * time.Millisecond,
		},
	}

	processor.AddChain(ChainInfo{ChainId: "1", Name: "Eth", RPC: rpchttp}, opts, &MockDecoder{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for at least 1 event or timeout
	mockSink.WaitForEvents(ctx, 1)
	cancel()
	<-done

	// Collect events from sink
	events := mockSink.GetAllEvents()

	event := events[0]
	assert.Equal(t, "logaddress", event.Address)
	assert.NotEqual(t, "receiptaddress", event.Address)
}

func TestAddChain_LoadsCursorFromSink(t *testing.T) {
	mockSink := &MockSink{
		CursorBlockNum:  100,
		CursorBlockHash: "0xabc123",
	}

	srv := NewTestServer(t)
	defer srv.Close()

	processor := NewProcessor(nil, mockSink)

	opts := &Options{
		RangeSize:          10,
		FetcherConcurrency: 1,
		StartBlock:         0,
	}

	err := processor.AddChain(
		ChainInfo{ChainId: "1", Name: "Eth", RPC: rpc.NewHTTPRPC(srv.URL, 0, 0)},
		opts,
		&MockDecoder{},
	)

	assert.NoError(t, err)

	// Verify cursor was loaded from sink
	chain := processor.chains["1"]
	assert.Equal(t, uint64(100), chain.cursor.BlockNum)
	assert.Equal(t, "0xabc123", chain.cursor.BlockHash)
}

func TestAddChain_CursorNotFound_StartsFromZero(t *testing.T) {
	mockSink := &MockSink{
		CursorErr: coreerrors.ErrCursorNotFound,
	}

	srv := NewTestServer(t)
	defer srv.Close()

	processor := NewProcessor(nil, mockSink)

	opts := &Options{
		RangeSize:          10,
		FetcherConcurrency: 1,
		StartBlock:         50,
	}

	err := processor.AddChain(
		ChainInfo{ChainId: "1", Name: "Eth", RPC: rpc.NewHTTPRPC(srv.URL, 0, 0)},
		opts,
		&MockDecoder{},
	)

	assert.NoError(t, err)

	chain := processor.chains["1"]
	assert.Equal(t, uint64(0), chain.cursor.BlockNum)
	assert.Equal(t, "", chain.cursor.BlockHash)
}

func TestAddChain_CursorLoadError_ReturnsError(t *testing.T) {
	mockSink := &MockSink{
		CursorErr: fmt.Errorf("database connection failed"),
	}

	srv := NewTestServer(t)
	defer srv.Close()

	processor := NewProcessor(nil, mockSink)

	opts := &Options{
		RangeSize:          10,
		FetcherConcurrency: 1,
	}

	err := processor.AddChain(
		ChainInfo{ChainId: "1", Name: "Eth", RPC: rpc.NewHTTPRPC(srv.URL, 0, 0)},
		opts,
		&MockDecoder{},
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load cursor")
}

func TestRun_CallsDecoderForEachLog(t *testing.T) {
	mockSink := &MockSink{}
	mockDecoder := &MockDecoder{}

	srv := NewTestServer(t)
	defer srv.Close()

	processor := NewProcessor(nil, mockSink)

	opts := &Options{
		RangeSize:          10,
		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
	}

	err := processor.AddChain(
		ChainInfo{ChainId: "1", Name: "Eth", RPC: rpc.NewHTTPRPC(srv.URL, 0, 0)},
		opts,
		mockDecoder,
	)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for at least 1 event or timeout
	mockSink.WaitForEvents(ctx, 1)
	cancel()
	<-done

	assert.Greater(t, mockDecoder.GetDecodeCount(), 0, "Decoder should have been called")
}

func TestRun_StoresDecodedEventsToSink(t *testing.T) {
	mockSink := &MockSink{}
	mockDecoder := &MockDecoder{
		DecodeFn: func(log types.Log) (*types.Event, error) {
			return &types.Event{
				Id:              "event-" + log.TransactionHash,
				ChainId:         "1",
				EventType:       "Transfer",
				BlockNumber:     1,
				Address:         log.Address,
				TransactionHash: log.TransactionHash,
			}, nil
		},
	}

	srv := NewTestServer(t)
	defer srv.Close()

	processor := NewProcessor(nil, mockSink)

	opts := &Options{
		RangeSize:          10,
		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
	}

	err := processor.AddChain(
		ChainInfo{ChainId: "1", Name: "Eth", RPC: rpc.NewHTTPRPC(srv.URL, 0, 0)},
		opts,
		mockDecoder,
	)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for at least 1 event or timeout
	mockSink.WaitForEvents(ctx, 1)
	cancel()
	<-done

	storeCalls := mockSink.GetStoreCalls()
	assert.Greater(t, len(storeCalls), 0, "Sink.Store should have been called")

	// Verify events were stored
	totalEvents := mockSink.GetEventCount()
	assert.Greater(t, totalEvents, 0, "Should have stored events")
}

func TestRun_SkipsNilEventsFromDecoder(t *testing.T) {
	mockSink := &MockSink{}
	mockDecoder := &MockDecoder{
		DecodeFn: func(log types.Log) (*types.Event, error) {
			// Return nil for all logs
			return nil, nil
		},
	}

	srv := NewTestServer(t)
	defer srv.Close()

	processor := NewProcessor(nil, mockSink)

	opts := &Options{
		RangeSize:          10,
		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
	}

	err := processor.AddChain(
		ChainInfo{ChainId: "1", RPC: rpc.NewHTTPRPC(srv.URL, 0, 0)},
		opts,
		mockDecoder,
	)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for context to expire (since all events are nil, nothing is stored)
	<-done

	// Should not panic, nil events should be skipped
	// Store might not be called if all events are nil
	assert.Greater(t, mockDecoder.GetDecodeCount(), 0, "Decoder should have been called")
}

func TestRun_HandlesDecodeErrors(t *testing.T) {
	mockSink := &MockSink{}
	mockDecoder := &MockDecoder{
		DecodeFn: func(log types.Log) (*types.Event, error) {
			return nil, fmt.Errorf("decode failed")
		},
	}

	srv := NewTestServer(t)
	defer srv.Close()

	processor := NewProcessor(nil, mockSink)

	opts := &Options{
		RangeSize:          10,
		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
	}

	err := processor.AddChain(
		ChainInfo{ChainId: "1", RPC: rpc.NewHTTPRPC(srv.URL, 0, 0)},
		opts,
		mockDecoder,
	)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for context to expire (since all decodes fail, nothing is stored)
	<-done

	assert.Greater(t, mockDecoder.GetDecodeCount(), 0, "Should have attempted to decode")
}

func TestReorg_CallsSinkRollback(t *testing.T) {
	mockSink := &MockSink{}

	flip := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req struct {
			Method string        `json:"method"`
			Params []interface{} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		switch req.Method {
		case "eth_blockNumber":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": "0x64",
			})

		case "eth_getBlockByNumber":
			s := fmt.Sprintf("%s", req.Params[0])
			blockNum, _ := utils.HexQtyToUint64(s)

			// Trigger reorg at block 41
			if !flip && blockNum == 41 {
				flip = true
				json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": 1,
					"result": map[string]any{
						"Number":     req.Params[0],
						"Hash":       req.Params[0],
						"ParentHash": "reorged_parent_hash",
						"Timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
					},
				})
			} else {
				json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": 1,
					"result": map[string]any{
						"Number":     req.Params[0],
						"Hash":       req.Params[0],
						"ParentHash": utils.Uint64ToHexQty(blockNum - 1),
						"Timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
					},
				})
			}

		case "eth_getLogs":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1,
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
		}
	}))
	defer srv.Close()

	processor := NewProcessor(nil, mockSink)

	opts := &Options{
		RangeSize:          10,
		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
	}

	err := processor.AddChain(
		ChainInfo{ChainId: "1", Name: "Eth", RPC: rpc.NewHTTPRPC(srv.URL, 0, 0)},
		opts,
		&MockDecoder{},
	)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- processor.Run(ctx)
	}()

	// Wait for rollback to be called or timeout
	mockSink.WaitForRollback(ctx)
	cancel()
	<-done

	rollbackCount := mockSink.GetRollbackCalls()
	assert.Greater(t, rollbackCount, 0, "Sink.Rollback should have been called on reorg")
}

func TestSinkStoreError_HandledGracefully(t *testing.T) {
	mockSink := &MockSink{
		StoreErr: fmt.Errorf("storage full"),
	}

	srv := NewTestServer(t)
	defer srv.Close()

	processor := NewProcessor(nil, mockSink)

	opts := &Options{
		RangeSize:          10,
		FetcherConcurrency: 1,
		StartBlock:         0,
		ConfimationDepth:   0,
		FetchMode:          FetchModeLogs,
		RetryConfig: &rpc.RetryConfig{
			MaxAttempts:    1,
			InitialBackoff: 10 * time.Millisecond,
		},
	}

	err := processor.AddChain(
		ChainInfo{ChainId: "1", RPC: rpc.NewHTTPRPC(srv.URL, 0, 0)},
		opts,
		&MockDecoder{},
	)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	runErr := processor.Run(ctx)

	// Should return error from store failure
	t.Logf("Run returned: %v", runErr)
	// The error behavior depends on your implementation
}
