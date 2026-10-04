package processor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryuux05/godex/pkg/core/decoder"
	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cursorLoadingSink struct {
	NoopSink
	loads  atomic.Int32
	loadFn func(context.Context) (uint64, string, error)
}

func (s *cursorLoadingSink) LoadCursor(ctx context.Context, _ string) (uint64, string, error) {
	s.loads.Add(1)
	if s.loadFn != nil {
		return s.loadFn(ctx)
	}
	return 0, "", nil
}

func TestInvalidConfigurationRejectedBeforeStorage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*Options)
		nilOpts bool
	}{
		{"nil options", nil, true},
		{"zero range", func(o *Options) { o.RangeSize = 0 }, false},
		{"negative range", func(o *Options) { o.RangeSize = -1 }, false},
		{"zero workers", func(o *Options) { o.FetcherConcurrency = 0 }, false},
		{"negative workers", func(o *Options) { o.FetcherConcurrency = -1 }, false},
		{"unknown fetch mode", func(o *Options) { o.FetchMode = "bad" }, false},
		{"negative polling", func(o *Options) { o.PollInterval = -1 }, false},
		{"negative pending ranges", func(o *Options) { o.MaxInFlightRanges = -1 }, false},
		{"negative batch", func(o *Options) { o.BatchSize = -1 }, false},
		{"zero attempts", func(o *Options) { o.RetryConfig = &rpc.RetryConfig{} }, false},
		{"negative timeout", func(o *Options) { c := rpc.DefaultRetryConfig(); c.PerRequestTimeout = -1; o.RetryConfig = &c }, false},
		{"invalid multiplier", func(o *Options) { c := rpc.DefaultRetryConfig(); c.Multiplier = math.NaN(); o.RetryConfig = &c }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &cursorLoadingSink{}
			p := NewProcessor(nil, s)
			opts := &Options{RangeSize: 2, FetcherConcurrency: 2}
			if tc.nilOpts {
				opts = nil
			} else {
				tc.change(opts)
			}
			assert.NotPanics(t, func() {
				assert.Error(t, p.AddChain(ChainInfo{ChainId: "1", RPC: &pipelineRPC{}}, opts, decoder.NewDecoderRouter()))
			})
			assert.Zero(t, s.loads.Load())
			assert.Empty(t, p.chains)
		})
	}
	for _, tc := range []struct {
		name   string
		chain  ChainInfo
		router *decoder.DecoderRouter
	}{
		{"missing ID", ChainInfo{RPC: &pipelineRPC{}}, decoder.NewDecoderRouter()},
		{"missing RPC", ChainInfo{ChainId: "1"}, decoder.NewDecoderRouter()},
		{"typed nil RPC", ChainInfo{ChainId: "1", RPC: (*pipelineRPC)(nil)}, decoder.NewDecoderRouter()},
		{"missing router", ChainInfo{ChainId: "1", RPC: &pipelineRPC{}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &cursorLoadingSink{}
			p := NewProcessor(nil, s)
			assert.Error(t, p.AddChain(tc.chain, &Options{RangeSize: 1, FetcherConcurrency: 1}, tc.router))
			assert.Zero(t, s.loads.Load())
		})
	}
	assert.Error(t, NewProcessor(nil, (*cursorLoadingSink)(nil)).AddChain(ChainInfo{ChainId: "1", RPC: &pipelineRPC{}}, &Options{RangeSize: 1, FetcherConcurrency: 1}, decoder.NewDecoderRouter()))
}

func TestChainConfigurationIsSnapshot(t *testing.T) {
	s := &cursorLoadingSink{}
	p := NewProcessor(nil, s)
	cfg := rpc.DefaultRetryConfig()
	opts := &Options{RangeSize: 2, FetcherConcurrency: 2, Topics: [][]string{{"event"}}, Addresses: []string{"0xabc"}, RetryConfig: &cfg}
	router := decoder.NewDecoderRouter().Register(decoder.ByTopic0("original"), "original", &MockDecoder{})
	require.NoError(t, p.AddChain(ChainInfo{ChainId: "1", RPC: &pipelineRPC{}}, opts, router))
	c := p.chains["1"]
	assert.Empty(t, opts.FetchMode)
	assert.Zero(t, opts.PollInterval)
	assert.Equal(t, time.Second, c.opts.PollInterval)
	assert.Equal(t, 4, c.opts.MaxInFlightRanges)
	opts.RangeSize = 0
	opts.Addresses[0] = "other"
	opts.Topics[0][0] = "other"
	cfg.MaxAttempts = 0
	router.Register(decoder.ByTopic0("later"), "later", &MockDecoder{})
	assert.Equal(t, 2, c.opts.RangeSize)
	assert.Equal(t, "0xabc", c.opts.Addresses[0])
	assert.Equal(t, "event", c.opts.Topics[0][0])
	assert.Equal(t, 3, c.opts.RetryConfig.MaxAttempts)
	event, err := c.router.Decode("1", types.Log{Topics: []string{"later"}})
	require.NoError(t, err)
	assert.Nil(t, event)
	assert.Error(t, p.AddChain(ChainInfo{ChainId: "1", RPC: &pipelineRPC{}}, &Options{RangeSize: 1, FetcherConcurrency: 1}, router))
	assert.Equal(t, int32(1), s.loads.Load())
}

func TestCursorLoadDeadline(t *testing.T) {
	s := &cursorLoadingSink{loadFn: func(ctx context.Context) (uint64, string, error) { <-ctx.Done(); return 0, "", ctx.Err() }}
	p := NewProcessor(nil, s)
	cfg := rpc.DefaultRetryConfig()
	cfg.PerRequestTimeout = 10 * time.Millisecond
	err := p.AddChain(ChainInfo{ChainId: "1", RPC: &pipelineRPC{}}, &Options{RangeSize: 1, FetcherConcurrency: 1, RetryConfig: &cfg}, decoder.NewDecoderRouter())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, p.chains)
}

func TestRunLifecycleAndIdlePolling(t *testing.T) {
	p, c, r, _ := newPipeline(t)
	c.opts.PollInterval = time.Hour
	called := make(chan struct{})
	var calls atomic.Int32
	r.headFn = func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			close(called)
		}
		return "0x0", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("no head request")
	}
	assert.ErrorContains(t, p.Run(context.Background()), "already running")
	assert.False(t, p.Status().StartTime.IsZero())
	assert.True(t, p.Status().Chains["1"].IsRunning)
	assert.Equal(t, int32(1), calls.Load(), "idle loop must not issue another immediate request")
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("polling delay ignored cancellation")
	}
	assert.False(t, p.Status().IsRunning)
	assert.False(t, p.Status().Chains["1"].IsRunning)
	assert.ErrorContains(t, NewProcessor(nil, NoopSink{}).Run(context.Background()), "no chains")
}

func TestLookupDuringRegistration(t *testing.T) {
	p := NewProcessor(nil, NoopSink{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			id := fmt.Sprint(i)
			_, _ = p.LookupChain(id)
			_, _ = p.IsLive(id)
			_ = p.GetChain(id)
		}
	}()
	for i := 0; i < 100; i++ {
		require.NoError(t, p.AddChain(ChainInfo{ChainId: fmt.Sprint(i), RPC: &pipelineRPC{}}, &Options{RangeSize: 1, FetcherConcurrency: 1}, decoder.NewDecoderRouter()))
	}
	wg.Wait()
	assert.Equal(t, ChainInfo{}, p.GetChain("missing"))
	_, err := p.LookupChain("missing")
	assert.Error(t, err)
}

func TestPlannerBoundsOutstandingRanges(t *testing.T) {
	p, c, r, _ := newPipeline(t)
	c.opts.MaxInFlightRanges = 2
	r.headFn = func(context.Context) (string, error) { return "0x64", nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs, _, err := p.planJobs(ctx, c)
	require.NoError(t, err)
	assert.Equal(t, BlockRange{1, 2}, <-jobs)
	assert.Equal(t, BlockRange{3, 4}, <-jobs)
	select {
	case <-jobs:
		t.Fatal("planner exceeded outstanding range bound")
	default:
	}
	<-c.pendingRanges // Simulate ordered commit acknowledgement.
	select {
	case job := <-jobs:
		assert.Equal(t, BlockRange{5, 6}, job)
	case <-time.After(time.Second):
		t.Fatal("commit did not unblock planning")
	}
}

func TestPlannerDoesNotOverflowAtMaximumHeight(t *testing.T) {
	p, c, r, _ := newPipeline(t)
	c.cursor.BlockNum = math.MaxUint64 - 2
	c.opts.RangeSize = 100
	r.headFn = func(context.Context) (string, error) { return "0xffffffffffffffff", nil }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	jobs, _, err := p.planJobs(ctx, c)
	require.NoError(t, err)
	assert.Equal(t, BlockRange{math.MaxUint64 - 1, math.MaxUint64}, <-jobs)
	select {
	case _, ok := <-jobs:
		assert.False(t, ok)
	case <-ctx.Done():
		t.Fatal("planner overflowed")
	}
}

func TestDecodeFailureStopsWindowUnlessExplicitlySkipped(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(fmt.Sprint(skip), func(t *testing.T) {
			p, c, _, s := newPipeline(t)
			failure := errors.New("malformed event")
			c.opts.SkipDecodeErrors = skip
			c.router = decoder.NewDecoderRouter().Register(func(types.Log) bool { return true }, "broken", &MockDecoder{DecodeFn: func(types.Log) (*types.Event, error) { return nil, failure }})
			var writes int
			s.updateFn = func(context.Context, string, uint64, string) error { writes++; return nil }
			err := p.processWindow(context.Background(), c, FetchResult{Range: BlockRange{1, 2}, Logs: []types.Log{{TransactionHash: "tx", LogIndex: "0x1"}}}, 2)
			if skip {
				require.NoError(t, err)
				assert.Equal(t, 1, writes)
			} else {
				assert.ErrorIs(t, err, failure)
				assert.Zero(t, writes)
				assert.Zero(t, c.cursor.BlockNum)
			}
		})
	}
}

func TestHealthIdleAndRollbackProgress(t *testing.T) {
	p, c, _, _ := newPipeline(t)
	p.isRunning = true
	c.progress.SetHead(10)
	c.opts.ConfirmationDepth = 2
	c.progress.Update(8, 7, time.Now().Add(-3*time.Minute))
	assert.True(t, p.Health().Healthy, "caught-up chain can wait without being stalled")
	c.progress.Update(7, 7, time.Now().Add(-3*time.Minute))
	assert.False(t, p.Health().Healthy)
	p.mu.Lock()
	c.progress.Rollback(3, time.Now())
	c.cursor.BlockNum = 3
	p.mu.Unlock()
	status := p.Status()
	assert.Equal(t, uint64(7), status.TotalEvents)
	assert.Equal(t, uint64(7), status.Chains["1"].BlocksBehind)
	p.setChainError(c, errors.New("transient"))
	assert.False(t, p.Health().Healthy)
	p.setChainError(c, nil)
	assert.True(t, p.Health().Healthy)
}
