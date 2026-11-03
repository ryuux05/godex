package godex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ryuux05/godex/pkg/core/decoder"
	"github.com/ryuux05/godex/pkg/godex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memorySink struct {
	events []godex.Event
	block  uint64
	hash   string
	cancel context.CancelFunc
}

func (s *memorySink) Store(_ context.Context, events []godex.Event) error {
	s.events = append(s.events, events...)
	last := events[len(events)-1]
	s.block = last.BlockNumber
	s.hash = last.BlockHash
	return nil
}
func (s *memorySink) LoadCursor(context.Context, string) (uint64, string, error) {
	return 0, "", godex.ErrCursorNotFound
}
func (s *memorySink) UpdateCursor(_ context.Context, _ string, n uint64, h string) error {
	s.block = n
	s.hash = h
	s.cancel()
	return nil
}
func (*memorySink) Rollback(context.Context, string, uint64, string) error {
	return fmt.Errorf("unexpected reorg")
}

func TestPublicSDKIndexesAndCommitsEmptyTail(t *testing.T) {
	const topic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var result any
		switch req.Method {
		case "eth_blockNumber":
			result = "0x2"
		case "eth_getBlockByNumber":
			var n string
			if err := json.Unmarshal(req.Params[0], &n); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			num := 1
			parent := "hash-0"
			if n == "0x2" {
				num = 2
				parent = "hash-1"
			}
			result = godex.Block{Number: n, Hash: fmt.Sprintf("hash-%d", num), ParentHash: parent}
		case "eth_getLogs":
			var f godex.Filter
			if err := json.Unmarshal(req.Params[0], &f); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			result = []godex.Log{}
			if f.FromBlock == "0x1" {
				result = []godex.Log{{Address: "0xabc", Topics: []string{topic, fmt.Sprintf("0x%064x", 1), fmt.Sprintf("0x%064x", 2)}, Data: fmt.Sprintf("0x%064x", 42), BlockNumber: "0x1", BlockHash: "hash-1", TransactionHash: "tx", LogIndex: "0x0"}}
			}
		default:
			http.Error(w, "unexpected RPC method", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := &memorySink{cancel: cancel}
	p := godex.NewProcessor(nil, s)
	p.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	d := decoder.NewStandardDecoder()
	require.NoError(t, d.RegisterABI("token", `[{"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256"}]}]`))
	router := decoder.NewDecoderRouter().Register(decoder.ByAddress("0xabc"), "token", d)
	cfg := godex.DefaultRetryConfig()
	cfg.MaxAttempts = 1
	require.NoError(t, p.AddChain(godex.ChainInfo{ChainId: "1", Name: "test", RPC: godex.NewHTTPRPC(srv.URL, 0, 0)}, &godex.Options{RangeSize: 1, FetcherConcurrency: 1, Topics: [][]string{{topic}}, RetryConfig: &cfg}, router))
	require.NoError(t, p.Run(ctx))
	require.Len(t, s.events, 1)
	ev := s.events[0]
	assert.Equal(t, "hash-1:tx:0", ev.Id)
	assert.Equal(t, "1", ev.ChainId)
	assert.Equal(t, "Transfer", ev.EventType)
	assert.Equal(t, big.NewInt(42), ev.Fields["value"])
	assert.Equal(t, uint64(2), s.block)
	assert.Equal(t, "hash-2", s.hash)
	assert.Equal(t, uint64(2), p.Status().Chains["1"].CursorBlock)
	assert.False(t, p.Status().IsRunning)
}

func TestPublicErrorClassification(t *testing.T) {
	wrapped := fmt.Errorf("indexing: %w", &godex.RPCError{Code: -32008, Message: "too large"})
	assert.True(t, godex.IsRetryableError(wrapped))
	assert.True(t, godex.IsResponseTooBigError(wrapped))
	assert.ErrorIs(t, fmt.Errorf("indexing: %w", &godex.ReorgError{BlockNum: 2}), godex.ErrReorgDetected)
}
