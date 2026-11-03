package postgres

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryuux05/godex/pkg/core/decoder"
	"github.com/ryuux05/godex/pkg/core/processor"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recoveryRPC changes canonical history deterministically; persistence and
// handler transactions use real PostgreSQL throughout this integration test.
type recoveryRPC struct {
	mu       sync.Mutex
	head     uint64
	fork     bool
	requests []types.Filter
}

func (r *recoveryRPC) advance(head uint64, fork bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.head = head
	r.fork = fork
}
func (r *recoveryRPC) hash(n uint64) string {
	branch := "A"
	if r.fork && n >= 3 {
		branch = "B"
	}
	return fmt.Sprintf("%s-%d", branch, n)
}
func (r *recoveryRPC) Head(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return utils.Uint64ToHexQty(r.head), nil
}
func (r *recoveryRPC) GetBlock(_ context.Context, number string) (types.Block, error) {
	n, err := utils.HexQtyToUint64(number)
	if err != nil {
		return types.Block{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return types.Block{Number: number, Hash: r.hash(n), ParentHash: r.hash(n - 1), Timestamp: "0x64"}, nil
}
func (r *recoveryRPC) GetBlocks(ctx context.Context, ns []string) (map[string]types.Block, error) {
	blocks := make(map[string]types.Block)
	for _, n := range ns {
		b, err := r.GetBlock(ctx, n)
		if err != nil {
			return nil, err
		}
		blocks[n] = b
	}
	return blocks, nil
}
func (r *recoveryRPC) GetLogs(_ context.Context, f types.Filter) ([]types.Log, error) {
	from, err := utils.HexQtyToUint64(f.FromBlock)
	if err != nil {
		return nil, err
	}
	to, err := utils.HexQtyToUint64(f.ToBlock)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, f)
	var logs []types.Log
	for n := from; n <= to; n++ {
		if n%2 == 0 {
			continue
		}
		hash := r.hash(n)
		logs = append(logs, types.Log{BlockNumber: utils.Uint64ToHexQty(n), BlockHash: hash, TransactionHash: "tx-" + hash, LogIndex: "0x0", Address: "0xabc", Topics: []string{utils.FunctionSignatureToTopic("Value(uint256)")}, Data: fmt.Sprintf("0x%064x", n)})
	}
	return logs, nil
}
func (r *recoveryRPC) GetBlockReceipts(context.Context, string) ([]types.Receipt, error) {
	return nil, fmt.Errorf("unexpected receipts request")
}

func TestProcessorPostgresReorgAndRestart(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()
	s, err := NewSink(SinkConfig{Pool: pool, Handler: &projectionHandler{}, CopyThreshold: 1})
	require.NoError(t, err)
	require.NoError(t, s.Migrate(ctx, "CREATE TABLE projection (id TEXT PRIMARY KEY, chain_id TEXT, block_num BIGINT)"))
	r := &recoveryRPC{head: 2}
	start := func() (*processor.Processor, func()) {
		d := decoder.NewStandardDecoder()
		require.NoError(t, d.RegisterABI("value", `[{"type":"event","name":"Value","inputs":[{"name":"value","type":"uint256"}]}]`))
		p := processor.NewProcessor(nil, s)
		p.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, p.AddChain(processor.ChainInfo{ChainId: "1", RPC: r}, &processor.Options{RangeSize: 2, FetcherConcurrency: 2, PollInterval: 5 * time.Millisecond, EnableTimestamps: true}, decoder.NewDecoderRouter().Register(func(types.Log) bool { return true }, "value", d)))
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- p.Run(runCtx) }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				select {
				case err := <-done:
					require.NoError(t, err)
				case <-time.After(5 * time.Second):
					t.Fatal("processor did not stop")
				}
			})
		}
		t.Cleanup(stop)
		return p, stop
	}
	wait := func(p *processor.Processor, height uint64, hash string) {
		require.Eventually(t, func() bool {
			n, h, err := s.LoadCursor(ctx, "1")
			return err == nil && n == height && h == hash && p.Status().Chains["1"].CursorBlock == height
		}, 5*time.Second, 5*time.Millisecond)
	}
	p, stop := start()
	wait(p, 2, "A-2") // only event at 1: cursor includes empty tail 2
	r.advance(4, false)
	wait(p, 4, "A-4")
	r.advance(6, true)
	wait(p, 6, "B-6") // parent mismatch at 5, canonical ancestor 2
	readIDs := func(table string) []string {
		rows, err := pool.Query(ctx, "SELECT "+map[string]string{"projection": "id", "chronicle_events": "event_id"}[table]+" FROM "+table+" ORDER BY block_num")
		require.NoError(t, err)
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		require.NoError(t, err)
		return ids
	}
	want := []string{"A-1:tx-A-1:0", "B-3:tx-B-3:0", "B-5:tx-B-5:0"}
	assert.Equal(t, want, readIDs("chronicle_events"))
	assert.Equal(t, want, readIDs("projection"))
	assert.Empty(t, p.Status().Chains["1"].LastError)
	stop()
	r.mu.Lock()
	r.requests = nil
	r.mu.Unlock()
	r.advance(8, true)
	restarted, stopRestarted := start()
	wait(restarted, 8, "B-8")
	stopRestarted()
	r.mu.Lock()
	requests := append([]types.Filter(nil), r.requests...)
	r.mu.Unlock()
	require.NotEmpty(t, requests)
	for _, request := range requests {
		n, err := utils.HexQtyToUint64(request.FromBlock)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, uint64(7), "restart must resume after durable sparse-window cursor")
	}
	want = append(want, "B-7:tx-B-7:0")
	assert.Equal(t, want, readIDs("chronicle_events"))
	assert.Equal(t, want, readIDs("projection"))
}
