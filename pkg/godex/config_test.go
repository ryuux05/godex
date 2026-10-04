package godex_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/pkg/godex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const serviceABI = `[{"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256"}]},{"type":"event","name":"Approval","inputs":[{"name":"owner","type":"address","indexed":true},{"name":"spender","type":"address","indexed":true},{"name":"value","type":"uint256"}]}]`
const serviceAddress = "0x0000000000000000000000000000000000000001"
const secondAddress = "0x0000000000000000000000000000000000000002"
const transferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
const approvalTopic = "0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925"

type setupSink struct {
	loads  int
	block  uint64
	hash   string
	events []godex.Event
	cancel context.CancelFunc
}

func (s *setupSink) LoadCursor(context.Context, string) (uint64, string, error) {
	s.loads++
	if s.block == 0 {
		return 0, "", godex.ErrCursorNotFound
	}
	return s.block, s.hash, nil
}
func (s *setupSink) Store(context.Context, []godex.Event) error {
	return errors.New("expected atomic window writes")
}
func (s *setupSink) UpdateCursor(context.Context, string, uint64, string) error {
	return errors.New("expected atomic window writes")
}
func (s *setupSink) Rollback(context.Context, string, uint64, string) error {
	return errors.New("unexpected rollback")
}
func (s *setupSink) StoreWindow(_ context.Context, _ string, n uint64, hash string, events []godex.Event) error {
	s.block, s.hash = n, hash
	s.events = append(s.events, events...)
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

type identityRPC struct {
	godex.RPC
	id     string
	err    error
	checks int
	wait   bool
}

func (r *identityRPC) ChainID(ctx context.Context) (string, error) {
	r.checks++
	if r.wait {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return r.id, r.err
}

type identitylessRPC struct{ godex.RPC }

func simpleConfig(s *setupSink, r godex.RPC) godex.Config {
	return godex.Config{ChainID: "1", RPC: r, Sink: s, Contracts: []godex.Contract{{Address: serviceAddress, ABI: serviceABI, Events: []string{"Transfer"}}}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestConfigRejectsMistakesBeforeIO(t *testing.T) {
	for name, modify := range map[string]func(*godex.Config){
		"missing chain":               func(c *godex.Config) { c.ChainID = "" },
		"negative chain":              func(c *godex.Config) { c.ChainID = "-1" },
		"missing sink":                func(c *godex.Config) { c.Sink = nil },
		"typed nil sink":              func(c *godex.Config) { c.Sink = (*setupSink)(nil) },
		"both sinks":                  func(c *godex.Config) { c.Postgres = &godex.PostgresConfig{} },
		"missing pool":                func(c *godex.Config) { c.Sink = nil; c.Postgres = &godex.PostgresConfig{} },
		"missing RPC":                 func(c *godex.Config) { c.RPC = nil },
		"typed nil RPC":               func(c *godex.Config) { c.RPC = (*identityRPC)(nil) },
		"RPC plus URL":                func(c *godex.Config) { c.RPCURL = "https://rpc.service" },
		"RPC plus HTTP options":       func(c *godex.Config) { c.RPCOptions = &godex.HTTPRPCOptions{} },
		"missing identity capability": func(c *godex.Config) { c.RPC = &identitylessRPC{} },
		"negative startup budget":     func(c *godex.Config) { c.StartupTimeout = -1 },
		"no contracts":                func(c *godex.Config) { c.Contracts = nil },
		"invalid address length":      func(c *godex.Config) { c.Contracts[0].Address = "0x123" },
		"invalid address hex":         func(c *godex.Config) { c.Contracts[0].Address = "0xgg00000000000000000000000000000000000000" },
		"invalid ABI":                 func(c *godex.Config) { c.Contracts[0].ABI = "[" },
		"missing event":               func(c *godex.Config) { c.Contracts[0].Events = []string{"NotAnEvent"} },
		"duplicate selection":         func(c *godex.Config) { c.Contracts[0].Events = []string{"Transfer", "Transfer"} },
		"duplicate route":             func(c *godex.Config) { c.Contracts = append(c.Contracts, c.Contracts[0]) },
		"negative tuning":             func(c *godex.Config) { c.Options = &godex.Options{RangeSize: -1} },
		"invalid fetch mode":          func(c *godex.Config) { c.Options = &godex.Options{FetchMode: "invalid"} },
		"cursor tuning":               func(c *godex.Config) { c.Options = &godex.Options{StartBlock: 1} },
		"confirmation tuning":         func(c *godex.Config) { c.Options = &godex.Options{ConfirmationDepth: 1} },
		"manual topics":               func(c *godex.Config) { c.Options = &godex.Options{Topics: [][]string{{transferTopic}}} },
		"manual addresses":            func(c *godex.Config) { c.Options = &godex.Options{Addresses: []string{serviceAddress}} },
		"no ABI events":               func(c *godex.Config) { c.Contracts[0].ABI = `[]` },
		"unnamed event": func(c *godex.Config) {
			c.Contracts[0].Events = nil
			c.Contracts[0].ABI = `[{"type":"event","inputs":[]}]`
		},
		"duplicate fields": func(c *godex.Config) {
			c.Contracts[0].Events = nil
			c.Contracts[0].ABI = `[{"type":"event","name":"X","inputs":[{"name":"a","type":"uint256"},{"name":"a","type":"bool"}]}]`
		},
		"unsupported selected ABI": func(c *godex.Config) {
			c.Contracts[0].Events = nil
			c.Contracts[0].ABI = `[{"type":"event","name":"X","inputs":[{"name":"a","type":"uint256[]"}]}]`
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, r := &setupSink{}, &identityRPC{id: "0x1"}
			c := simpleConfig(s, r)
			modify(&c)
			assert.Error(t, c.Validate())
			_, err := godex.New(context.Background(), c)
			assert.Error(t, err)
			assert.Zero(t, s.loads)
			assert.Zero(t, r.checks)
		})
	}
}

func TestEndpointIdentityAndStartupCancellation(t *testing.T) {
	for _, id := range []string{"0x2", "invalid", "", "-1"} {
		s, r := &setupSink{}, &identityRPC{id: id}
		c := simpleConfig(s, r)
		require.NoError(t, c.Validate())
		assert.Zero(t, r.checks)
		_, err := godex.New(context.Background(), c)
		require.Error(t, err)
		assert.Zero(t, s.loads)
	}
	s, r := &setupSink{}, &identityRPC{wait: true}
	c := simpleConfig(s, r)
	c.StartupTimeout = 20 * time.Millisecond
	_, err := godex.New(context.Background(), c)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Zero(t, s.loads)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.wait = false
	r.checks = 0
	_, err = godex.New(ctx, c)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, r.checks)
	_, err = godex.New(nil, c)
	assert.ErrorContains(t, err, "context")
	expected := errors.New("identity unavailable")
	r.err = expected
	_, err = godex.New(context.Background(), c)
	assert.ErrorIs(t, err, expected)
	assert.Zero(t, s.loads)
	c.RPC = &identitylessRPC{}
	c.SkipChainIDCheck = true
	_, err = godex.New(context.Background(), c)
	require.NoError(t, err)
	assert.Equal(t, 1, s.loads)
}

func TestFirstBlockAndResumeSemantics(t *testing.T) {
	for _, tc := range []struct {
		from, stored, want uint64
		hash               string
	}{
		{0, 0, 0, ""}, {1, 0, 0, ""}, {5, 0, 4, ""}, {5, 3, 4, ""}, {5, 4, 4, "stored-hash"}, {5, 10, 10, "stored-hash"}, {0, 10, 10, "stored-hash"}, {math.MaxUint64, 0, math.MaxUint64 - 1, ""},
	} {
		t.Run(fmt.Sprintf("%d/%d", tc.from, tc.stored), func(t *testing.T) {
			s := &setupSink{block: tc.stored, hash: "stored-hash"}
			c := simpleConfig(s, &identityRPC{id: "0x1"})
			c.FromBlock = tc.from
			p, err := godex.New(context.Background(), c)
			require.NoError(t, err)
			status := p.Status().Chains["1"]
			assert.Equal(t, tc.want, status.CursorBlock)
			assert.Equal(t, tc.hash, status.CursorHash)
			assert.Equal(t, 1000, status.RangeSize)
			assert.Equal(t, 4, status.FetcherConcurrency)
		})
	}
	a, b := godex.DefaultOptions(), godex.DefaultOptions()
	a.RetryConfig.MaxAttempts = 999
	assert.NotEqual(t, a.RetryConfig.MaxAttempts, b.RetryConfig.MaxAttempts)
	require.NoError(t, b.Validate())
}

func TestEventSelectionSupportsOverloadsAndUnselectedUnsupportedEvents(t *testing.T) {
	s := &setupSink{}
	c := simpleConfig(s, &identityRPC{id: "0x1"})
	c.Contracts[0].ABI = `[{"type":"event","name":"X","inputs":[{"name":"a","type":"uint256"}]},{"type":"event","name":"X","inputs":[{"name":"a","type":"address"}]},{"type":"event","name":"Unsupported","inputs":[{"name":"a","type":"uint256[]"}]}]`
	c.Contracts[0].Events = []string{"X"}
	assert.ErrorContains(t, c.Validate(), "canonical signature")
	c.Contracts[0].Events = []string{"X(uint256)"}
	require.NoError(t, c.Validate())
	c.Contracts[0].Events = nil
	assert.ErrorContains(t, c.Validate(), "Unsupported")
	c.Contracts[0].ABI = serviceABI
	require.NoError(t, c.Validate())
}

func TestPublicConfigDerivesFiltersRoutesAndSnapshotsSettings(t *testing.T) {
	var mu sync.Mutex
	var gotFilter godex.Filter
	identities := 0
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
		case "eth_chainId":
			identities++
			result = "0x1"
		case "eth_blockNumber":
			result = "0x2"
		case "eth_getBlockByNumber":
			var n string
			_ = json.Unmarshal(req.Params[0], &n)
			parent := "hash-1"
			hash := "hash-2"
			if n == "0x1" {
				hash, parent = "hash-1", "hash-0"
			}
			result = godex.Block{Number: n, Hash: hash, ParentHash: parent}
		case "eth_getLogs":
			mu.Lock()
			_ = json.Unmarshal(req.Params[0], &gotFilter)
			mu.Unlock()
			var logs []godex.Log
			for i, pair := range [][2]string{{serviceAddress, transferTopic}, {secondAddress, approvalTopic}, {serviceAddress, approvalTopic}, {secondAddress, transferTopic}} {
				logs = append(logs, godex.Log{Address: pair[0], Topics: []string{pair[1], fmt.Sprintf("0x%064x", 1), fmt.Sprintf("0x%064x", 2)}, Data: fmt.Sprintf("0x%064x", 42), BlockNumber: "0x2", BlockHash: "hash-2", TransactionHash: "tx", LogIndex: fmt.Sprintf("0x%x", i)})
			}
			result = logs
		default:
			http.Error(w, "unexpected method", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := &setupSink{cancel: cancel}
	c := simpleConfig(s, nil)
	c.RPCURL = srv.URL
	c.ChainID = "0x1"
	c.FromBlock = 2
	c.RPCOptions = &godex.HTTPRPCOptions{Client: srv.Client()}
	c.Contracts = append(c.Contracts, godex.Contract{Address: secondAddress, ABI: serviceABI, Events: []string{"Approval"}})
	c.Options = &godex.Options{RangeSize: 1, FetcherConcurrency: 1}
	require.NoError(t, c.Validate())
	assert.Zero(t, identities)
	p, err := godex.New(ctx, c)
	require.NoError(t, err)
	c.Contracts[0].Address = secondAddress
	c.Contracts[0].Events[0] = "Approval"
	c.Options.RangeSize = 999
	require.NoError(t, p.Run(ctx))
	require.Len(t, s.events, 2)
	assert.Equal(t, "Transfer", s.events[0].EventType)
	assert.Equal(t, "Approval", s.events[1].EventType)
	assert.Equal(t, "1", s.events[0].ChainId)
	value, err := s.events[0].Fields.Uint64("value")
	require.NoError(t, err)
	assert.Equal(t, uint64(42), value)
	assert.Equal(t, uint64(2), s.block)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "0x2", gotFilter.FromBlock)
	assert.Equal(t, "0x2", gotFilter.ToBlock)
	assert.ElementsMatch(t, []string{serviceAddress, secondAddress}, gotFilter.Address)
	require.Len(t, gotFilter.Topics, 1)
	assert.ElementsMatch(t, []string{transferTopic, approvalTopic}, gotFilter.Topics[0])
	assert.Equal(t, 1, p.Status().Chains["1"].RangeSize)
}

func TestWrongNetworkDoesNotInitializePostgres(t *testing.T) {
	r := &identityRPC{id: "0x2"}
	c := simpleConfig(&setupSink{}, r)
	c.Sink = nil
	c.Postgres = &godex.PostgresConfig{Pool: &pgxpool.Pool{}}
	_, err := godex.New(context.Background(), c)
	assert.ErrorContains(t, err, "does not match")
}
