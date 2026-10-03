package processor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ryuux05/godex/pkg/core/decoder"
	coreerrors "github.com/ryuux05/godex/pkg/core/errors"
	"github.com/ryuux05/godex/pkg/core/metrics"
	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These fakes let pipeline tests control completion and failure without real
// HTTP servers, polling loops, or waits for a live chain.
type pipelineRPC struct {
	headFn     func(context.Context) (string, error)
	blockFn    func(context.Context, string) (types.Block, error)
	logsFn     func(context.Context, types.Filter) ([]types.Log, error)
	blocksFn   func(context.Context, []string) (map[string]types.Block, error)
	receiptsFn func(context.Context, string) ([]types.Receipt, error)
}

func (r *pipelineRPC) Head(ctx context.Context) (string, error) {
	if r.headFn != nil {
		return r.headFn(ctx)
	}
	return "0x4", nil
}
func (r *pipelineRPC) GetBlock(ctx context.Context, n string) (types.Block, error) {
	if r.blockFn != nil {
		return r.blockFn(ctx, n)
	}
	bn, err := utils.HexQtyToUint64(n)
	return types.Block{Number: n, Hash: fmt.Sprintf("hash-%d", bn), ParentHash: fmt.Sprintf("hash-%d", bn-1), Timestamp: "0x64"}, err
}
func (r *pipelineRPC) GetLogs(ctx context.Context, f types.Filter) ([]types.Log, error) {
	if r.logsFn != nil {
		return r.logsFn(ctx, f)
	}
	return nil, nil
}
func (r *pipelineRPC) GetBlocks(ctx context.Context, ns []string) (map[string]types.Block, error) {
	if r.blocksFn != nil {
		return r.blocksFn(ctx, ns)
	}
	return nil, errors.New("unexpected batch call")
}
func (r *pipelineRPC) GetBlockReceipts(ctx context.Context, n string) ([]types.Receipt, error) {
	if r.receiptsFn != nil {
		return r.receiptsFn(ctx, n)
	}
	return nil, errors.New("unexpected receipts call")
}

type pipelineSink struct {
	NoopSink
	storeFn    func(context.Context, []types.Event) error
	updateFn   func(context.Context, string, uint64, string) error
	rollbackFn func(context.Context, string, uint64, string) error
}

func (s *pipelineSink) Store(ctx context.Context, es []types.Event) error {
	if s.storeFn != nil {
		return s.storeFn(ctx, es)
	}
	return nil
}
func (s *pipelineSink) UpdateCursor(ctx context.Context, id string, n uint64, h string) error {
	if s.updateFn != nil {
		return s.updateFn(ctx, id, n, h)
	}
	return nil
}
func (s *pipelineSink) Rollback(ctx context.Context, id string, n uint64, h string) error {
	if s.rollbackFn != nil {
		return s.rollbackFn(ctx, id, n, h)
	}
	return nil
}

type pipelineMetrics struct {
	metrics.Noop
	lag uint64
}

func (m *pipelineMetrics) ObservedBlockLag(_ string, lag uint64) { m.lag = lag }

func newPipeline(t *testing.T) (*Processor, *chainState, *pipelineRPC, *pipelineSink) {
	t.Helper()
	r := &pipelineRPC{}
	s := &pipelineSink{}
	p := NewProcessor(nil, s)
	p.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	cfg := rpc.DefaultRetryConfig()
	cfg.MaxAttempts = 1
	opts := &Options{RangeSize: 2, FetcherConcurrency: 2, RetryConfig: &cfg}
	router := decoder.NewDecoderRouter().Register(func(types.Log) bool { return true }, "test", &MockDecoder{})
	require.NoError(t, p.AddChain(ChainInfo{ChainId: "1", Name: "test", RPC: r}, opts, router))
	return p, p.chains["1"], r, s
}

func TestPlanJobsBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                string
		head, cursor, depth uint64
		live                bool
		target              uint64
		ranges              []BlockRange
		wantLive            bool
	}{
		{"historical tail", 10, 1, 2, false, 8, []BlockRange{{2, 3}, {4, 5}, {6, 7}, {8, 8}}, false},
		{"live single blocks", 5, 1, 1, true, 4, []BlockRange{{2, 2}, {3, 3}, {4, 4}}, true},
		{"caught up", 5, 4, 1, false, 4, nil, true},
		{"head below confirmations", 2, 0, 3, false, 0, nil, true},
		{"head equals confirmations", 3, 0, 3, false, 0, nil, true},
		{"cursor ahead", 5, 8, 1, false, 4, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, c, r, _ := newPipeline(t)
			r.headFn = func(context.Context) (string, error) { return utils.Uint64ToHexQty(tc.head), nil }
			c.cursor.BlockNum = tc.cursor
			c.opts.ConfirmationDepth = tc.depth
			c.isLive.Store(tc.live)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			jobs, target, err := p.planJobs(ctx, c)
			require.NoError(t, err)
			assert.Equal(t, tc.target, target)
			var got []BlockRange
			for {
				select {
				case job, ok := <-jobs:
					if !ok {
						assert.Equal(t, tc.ranges, got)
						assert.Equal(t, tc.wantLive, c.isLive.Load())
						assert.Equal(t, tc.head, c.progress.Snapshot().head)
						return
					}
					got = append(got, job)
				case <-ctx.Done():
					t.Fatal("planner did not finish")
				}
			}
		})
	}
}

func TestPlanJobsErrorsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, head string
		err        error
	}{{"RPC error", "", errors.New("head unavailable")}, {"malformed head", "0xgg", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			p, c, r, _ := newPipeline(t)
			r.headFn = func(context.Context) (string, error) { return tc.head, tc.err }
			jobs, _, err := p.planJobs(context.Background(), c)
			assert.Error(t, err)
			assert.Nil(t, jobs)
		})
	}
	p, c, r, _ := newPipeline(t)
	r.headFn = func(context.Context) (string, error) { return "0x100", nil }
	c.opts.FetcherConcurrency = 0
	ctx, cancel := context.WithCancel(context.Background())
	jobs, _, err := p.planJobs(ctx, c)
	require.NoError(t, err)
	cancel()
	// A ready receiver and cancellation may both win the select. Drain any
	// in-flight ranges and require the planner to close promptly.
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-jobs:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("blocked planner ignored cancellation")
		}
	}
}

func TestReceiptTopicFilter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter [][]string
		topics []string
		want   bool
	}{
		{"unfiltered", nil, nil, true},
		{"missing signature", [][]string{{"a"}}, nil, false},
		{"OR within position", [][]string{{"a", "b"}}, []string{"b"}, true},
		{"all positions match", [][]string{{"a"}, {"b"}}, []string{"a", "b"}, true},
		{"later position mismatches", [][]string{{"a"}, {"b"}}, []string{"a", "c"}, false},
		{"earlier position mismatches", [][]string{{"a"}, {"b"}}, []string{"c", "b"}, false},
		{"missing later position", [][]string{{"a"}, {"b"}}, []string{"a"}, false},
		{"wildcard position", [][]string{nil, {"b"}}, []string{"a", "b"}, true},
		{"all wildcards", [][]string{nil, nil}, []string{"a", "b"}, true},
		{"wildcard still requires position", [][]string{{"a"}, nil}, []string{"a"}, false},
		{"case insensitive", [][]string{{"0xAB"}}, []string{"0xab"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProcessor(nil, NoopSink{})
			c := &chainState{opts: &Options{Topics: tc.filter}, topics: tc.filter}
			assert.Equal(t, tc.want, p.matchesTopicFilter(types.Log{Topics: tc.topics}, c))
		})
	}
}

func TestFetchReceiptsFiltersAddressesAndTopics(t *testing.T) {
	p, c, r, _ := newPipeline(t)
	c.addressSet = map[string]struct{}{"abc": {}}
	c.topics = [][]string{{"event"}, {"owner"}}
	c.opts.Topics = c.topics
	var requested []string
	r.receiptsFn = func(ctx context.Context, n string) ([]types.Receipt, error) {
		_, ok := ctx.Deadline()
		assert.True(t, ok)
		requested = append(requested, n)
		return []types.Receipt{{Logs: []types.Log{
			{Address: "0xAbC", Topics: []string{"event", "owner"}, BlockNumber: n},
			{Address: "0xdef", Topics: []string{"event", "owner"}},
			{Address: "0xabc", Topics: []string{"event", "other"}},
		}}}, nil
	}
	logs, err := p.fetchLogsFromReceipts(context.Background(), 1, 2, c)
	require.NoError(t, err)
	assert.Equal(t, []string{"0x1", "0x2"}, requested)
	require.Len(t, logs, 2)
	assert.Equal(t, "0x2", logs[1].BlockNumber)
	c.addressSet = nil
	c.topics = nil
	c.opts.Topics = nil
	logs, err = p.fetchLogsFromReceipts(context.Background(), 1, 1, c)
	require.NoError(t, err)
	assert.Len(t, logs, 3)
}

func TestFetchTimestamps(t *testing.T) {
	p, c, r, _ := newPipeline(t)
	r.blocksFn = func(ctx context.Context, ns []string) (map[string]types.Block, error) {
		assert.ElementsMatch(t, []string{"0x1", "0x2"}, ns)
		_, ok := ctx.Deadline()
		assert.True(t, ok)
		return map[string]types.Block{"0x1": {Timestamp: "0x64"}, "0x2": {Timestamp: "0xc8"}}, nil
	}
	ts, err := p.fetchTimestamps(context.Background(), c, []types.Log{{BlockNumber: "0x1"}, {BlockNumber: "0x1"}, {BlockNumber: "0x2"}, {BlockNumber: "invalid"}})
	require.NoError(t, err)
	assert.Equal(t, map[uint64]uint64{1: 100, 2: 200}, ts)
	for _, tc := range []struct {
		name   string
		blocks map[string]types.Block
		err    error
	}{
		{"RPC failure", nil, errors.New("batch unavailable")},
		{"malformed block", map[string]types.Block{"bad": {Timestamp: "0x1"}}, nil},
		{"malformed timestamp", map[string]types.Block{"0x1": {Timestamp: "bad"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.blocksFn = func(context.Context, []string) (map[string]types.Block, error) { return tc.blocks, tc.err }
			_, err := p.fetchTimestamps(context.Background(), c, []types.Log{{BlockNumber: "0x1"}})
			assert.Error(t, err)
		})
	}
}

func TestArbiterCommitsOutOfOrderResultsInOrder(t *testing.T) {
	p, c, _, s := newPipeline(t)
	var committed []uint64
	s.updateFn = func(_ context.Context, _ string, n uint64, _ string) error {
		committed = append(committed, n)
		return nil
	}
	results := make(chan FetchResult, 3)
	for _, br := range []BlockRange{{5, 6}, {3, 4}, {1, 2}} {
		results <- FetchResult{Range: br}
	}
	close(results)
	done, errs := p.arbiter(context.Background(), c, results, 6)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("arbiter did not finish")
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
	assert.Equal(t, []uint64{2, 4, 6}, committed)
	assert.Equal(t, uint64(6), c.cursor.BlockNum)
	assert.Equal(t, "hash-6", c.cursor.BlockHash)
}

func TestProcessWindowFailureDoesNotAdvanceProgress(t *testing.T) {
	for _, stage := range []string{"header", "end header", "cursor", "store"} {
		t.Run(stage, func(t *testing.T) {
			p, c, r, s := newPipeline(t)
			failure := errors.New("write failed")
			result := FetchResult{Range: BlockRange{1, 2}}
			switch stage {
			case "header":
				r.blockFn = func(context.Context, string) (types.Block, error) { return types.Block{}, failure }
			case "end header":
				r.blockFn = func(_ context.Context, n string) (types.Block, error) {
					if n == "0x2" {
						return types.Block{}, failure
					}
					return types.Block{Hash: "hash-1"}, nil
				}
			case "cursor":
				s.updateFn = func(context.Context, string, uint64, string) error { return failure }
			case "store":
				result.Logs = []types.Log{{BlockNumber: "0x1"}}
				s.storeFn = func(context.Context, []types.Event) error { return failure }
			}
			assert.ErrorIs(t, p.processWindow(context.Background(), c, result, 2), failure)
			assert.Zero(t, c.cursor.BlockNum)
			assert.Empty(t, c.cursor.BlockHash)
			assert.Zero(t, c.blockHashCache.Len())
			assert.Zero(t, c.progress.Snapshot().current)
			assert.Zero(t, c.progress.Snapshot().events)
		})
	}
}

func TestProcessBatchPropagatesFailures(t *testing.T) {
	for _, stage := range []string{"fetch", "cursor", "store"} {
		t.Run(stage, func(t *testing.T) {
			p, c, r, s := newPipeline(t)
			failure := errors.New("pipeline failure")
			c.opts.FetcherConcurrency = 1
			switch stage {
			case "fetch":
				r.logsFn = func(context.Context, types.Filter) ([]types.Log, error) { return nil, failure }
			case "cursor":
				s.updateFn = func(context.Context, string, uint64, string) error { return failure }
			case "store":
				r.logsFn = func(context.Context, types.Filter) ([]types.Log, error) {
					return []types.Log{{BlockNumber: "0x1"}}, nil
				}
				s.storeFn = func(context.Context, []types.Event) error { return failure }
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			assert.ErrorIs(t, p.processBatch(ctx, c), failure)
			assert.Zero(t, c.cursor.BlockNum)
		})
	}
}

func TestProcessBatchLagUsesConfirmedTarget(t *testing.T) {
	p, c, r, _ := newPipeline(t)
	r.headFn = func(context.Context) (string, error) { return "0x64", nil }
	c.opts.ConfirmationDepth = 10
	c.cursor.BlockNum = 90
	m := &pipelineMetrics{}
	p.metrics = m
	require.NoError(t, p.processBatch(context.Background(), c))
	assert.Zero(t, m.lag)
}

func TestStatusWhileCommitting(t *testing.T) {
	p, c, _, _ := newPipeline(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				p.Status()
				p.Health()
			}
		}
	}()
	for n := uint64(1); n <= 30; n++ {
		require.NoError(t, p.processWindow(context.Background(), c, FetchResult{Range: BlockRange{n, n}}, 30))
	}
	cancel()
	wg.Wait()
}

func TestDetectReorgKeepsCanonicalAncestor(t *testing.T) {
	p, c, r, s := newPipeline(t)
	c.cursor = &cursorState{BlockNum: 4, BlockHash: "old-4"}
	c.blockHashCache.Set(2, "hash-2")
	c.blockHashCache.Set(4, "old-4")
	r.blockFn = func(_ context.Context, n string) (types.Block, error) {
		if n == "0x3" {
			return types.Block{ParentHash: "hash-2"}, nil
		}
		return types.Block{ParentHash: "new-4"}, nil
	}
	var rollbackAt uint64
	s.rollbackFn = func(_ context.Context, id string, n uint64, h string) error {
		assert.Equal(t, "1", id)
		assert.Equal(t, "hash-2", h)
		rollbackAt = n
		return nil
	}
	err := p.detectReorg(context.Background(), c, 5, types.Block{Hash: "hash-5", ParentHash: "new-4"})
	assert.ErrorIs(t, err, coreerrors.ErrReorgDetected)
	assert.Equal(t, uint64(3), rollbackAt, "rollback is inclusive, so delete starting after the canonical ancestor")
	assert.Equal(t, &cursorState{BlockNum: 2, BlockHash: "hash-2"}, c.cursor)
}

func TestFetchAllCancelsWorkerWaitingForJobs(t *testing.T) {
	p, c, _, _ := newPipeline(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := make(chan BlockRange)
	results, done, err := p.fetchAll(ctx, c, jobs)
	require.NoError(t, err)
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("workers waiting for jobs ignored cancellation")
	}
	_, ok := <-results
	assert.False(t, ok)
}

func TestProcessBatchFetchFailureCancelsOtherWorkers(t *testing.T) {
	p, c, r, _ := newPipeline(t)
	failure := errors.New("fetch unavailable")
	peerStarted := make(chan struct{})
	r.logsFn = func(ctx context.Context, f types.Filter) ([]types.Log, error) {
		if f.FromBlock == "0x1" {
			select {
			case <-peerStarted:
				return nil, failure
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		close(peerStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assert.ErrorIs(t, p.processBatch(ctx, c), failure)
	assert.Zero(t, c.cursor.BlockNum)
}

func TestRunTreatsWrappedCancellationAsShutdown(t *testing.T) {
	p, c, r, _ := newPipeline(t)
	r.headFn = func(context.Context) (string, error) {
		return "", fmt.Errorf("head: %w", context.Canceled)
	}
	assert.NoError(t, p.runChain(context.Background(), c))
}
