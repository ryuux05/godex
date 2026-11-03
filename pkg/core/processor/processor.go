package processor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ryuux05/godex/pkg/core/decoder"
	coreerrors "github.com/ryuux05/godex/pkg/core/errors"
	"github.com/ryuux05/godex/pkg/core/metrics"
	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/sink"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"

	"golang.org/x/sync/errgroup"
)

const (
	// block to fallback during reorg incase there is no ancestor found
	DefaultHardFallbackBlocks = 1000
)

type chainState struct {
	// chainInfo stores chain information where the indexer going to query
	// Specify RPC (endpoint and rate-limit)
	chainInfo ChainInfo
	// Cursor used when there is cursor state in persistance storage
	// If Cursor exists, processor will ignore StartBlock and use
	// Cursor.BlockNum instead.
	// Existing cursor also means that the processor are resuming the indexing process
	cursor *cursorState
	// LRU cache to store block hash to compare the next block parent hash
	blockHashCache *BlockHashCache
	// The number of block that we will fall back to in case we couldnt resolve reorg
	hardFallbackBlocks uint64
	// Storage to store the formatted topics
	topics [][]string
	// Map of whitelisted contract for processor to queue
	addressSet map[string]struct{}
	// List of whitelisted contract addresses
	addresses []string
	// State of the processor of each chain
	// Is it syncing historical block or live block
	isLive  atomic.Bool
	running atomic.Bool
	// A permit remains occupied until its range is committed, bounding reordering.
	pendingRanges chan struct{}
	// options for processor
	opts *Options
	//chain Progress
	progress *chainProgress
	// router for decoding logs for this chain
	router *decoder.DecoderRouter
	// store the last err occured
	lastErr string
	// store time last error occured
	lastErrAt time.Time
}

type Processor struct {
	// chains is an internal per-chain state
	// It's a map with chainId as key.
	chains map[string]*chainState
	// logsChan is a channel where processor will store the indexed logs
	// It's a map with chainId as key.
	//logsCh map[string]chan types.Log

	// isRunning track the processor state if it's running or stopped.
	// False by default until the processor run.
	isRunning bool
	startTime time.Time
	// Mutex to access data safely
	mu sync.RWMutex
	// metrics
	metrics metrics.Metrics
	// Sink is a persistance storage
	sink sink.Sink
	// logger is for strucutured logging
	logger *slog.Logger
}

func NewProcessor(m metrics.Metrics, s sink.Sink) *Processor {
	if nilInterface(m) {
		m = metrics.Noop{}
	}
	return &Processor{
		chains:    make(map[string]*chainState),
		metrics:   m,
		sink:      s,
		logger:    slog.Default(),
		isRunning: false,
	}
}

func (p *Processor) AddChain(chain ChainInfo, opts *Options, router *decoder.DecoderRouter) error {
	return p.AddChainContext(context.Background(), chain, opts, router)
}

// AddChainContext bounds cursor loading and validates before touching storage.
func (p *Processor) AddChainContext(ctx context.Context, chain ChainInfo, opts *Options, router *decoder.DecoderRouter) error {
	if nilInterface(p.sink) || nilInterface(chain.RPC) || router == nil {
		return fmt.Errorf("sink, RPC and decoder router are required")
	}
	if chain.ChainId == "" {
		return fmt.Errorf("chain ID is required")
	}
	normalized, err := normalizeOptions(opts)
	if err != nil {
		return err
	}
	p.mu.RLock()
	logger := p.logger
	running := p.isRunning
	_, exists := p.chains[chain.ChainId]
	p.mu.RUnlock()
	if running || exists {
		return fmt.Errorf("cannot add chain while running or replace registered chain %s", chain.ChainId)
	}
	loadCtx, cancel := context.WithTimeout(ctx, normalized.RetryConfig.PerRequestTimeout)
	defer cancel()
	blockNum, blockHash, err := p.sink.LoadCursor(loadCtx, chain.ChainId)
	logger.Info("cursor loaded from sink",
		slog.String("chain_id", chain.ChainId),
		slog.Uint64("loaded_block_num", blockNum),
		slog.String("loaded_block_hash", blockHash),
		slog.Uint64("start_block", normalized.StartBlock),
		slog.Any("error", err))

	if err != nil {
		// If cursor not found (clean db), start from block 0
		if errors.Is(err, coreerrors.ErrCursorNotFound) {
			blockNum = 0
			blockHash = ""
		} else {
			return fmt.Errorf("failed to load cursor: %w", err)
		}
	}

	if blockNum <= 0 {
		blockNum = 0
	}

	return p.addChain(chain, normalized, blockNum, blockHash, router.Clone())
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// SetLogger configures logging before Run. Calls during Run are ignored.
func (p *Processor) SetLogger(l *slog.Logger) {
	if l == nil {
		l = slog.Default()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.isRunning {
		return
	}
	p.logger = l
}

func (p *Processor) GetChain(chainId string) ChainInfo {
	chain, _ := p.LookupChain(chainId)
	return chain
}

func (p *Processor) LookupChain(chainId string) (ChainInfo, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	chain, ok := p.chains[chainId]
	if !ok {
		return ChainInfo{}, fmt.Errorf("chain %s not found", chainId)
	}
	return chain.chainInfo, nil
}

func (p *Processor) Run(ctx context.Context) error {
	p.mu.Lock()
	if p.isRunning {
		p.mu.Unlock()
		return fmt.Errorf("processor is already running")
	}
	if len(p.chains) == 0 {
		p.mu.Unlock()
		return fmt.Errorf("no chains registered")
	}
	p.isRunning = true
	p.startTime = time.Now()
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.isRunning = false
		p.mu.Unlock()
	}()

	g := errgroup.Group{}
	for chainId, chain := range p.chains {
		id := chainId
		c := chain
		//ch := p.logsCh[id]

		g.Go(func() error {
			err := p.runChain(ctx, c)
			if err != nil {
				p.logger.Error("chain stopped", slog.String("chain_id", id), slog.Any("error", err))
			}
			return err
		})

	}

	return g.Wait()
}

func (p *Processor) addChain(chain ChainInfo, opts *Options, blockNum uint64, blockHash string, router *decoder.DecoderRouter) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isRunning {
		return fmt.Errorf("cannot add chain while processor is running")
	}
	if _, exists := p.chains[chain.ChainId]; exists {
		return fmt.Errorf("chain %s is already registered", chain.ChainId)
	}

	startBlock := opts.StartBlock
	if startBlock <= 0 {
		startBlock = 0
	}

	var cursor *cursorState = &cursorState{}
	if blockNum > 0 {

		if blockNum >= startBlock {
			cursor.BlockNum = blockNum
			cursor.BlockHash = blockHash
		} else {

			cursor.BlockNum = startBlock
			cursor.BlockHash = ""
		}
	} else {

		cursor.BlockNum = startBlock
		cursor.BlockHash = ""
	}

	// Clamp the max storedwindowhash bound.
	rs := uint64(opts.RangeSize) // assume >0
	base := opts.ReorgLookbackBlocks / rs
	if opts.ReorgLookbackBlocks%rs != 0 {
		base++
	}
	if base > 255 {
		base = 255
	}
	cap := base + 1
	if cap < 8 {
		cap = 8
	}
	if cap > 256 {
		cap = 256
	}

	// Convert topics to keccak signature
	topics := utils.ConvertToTopics(opts.Topics)

	// normalize all addresses before storing it
	addressSet := make(map[string]struct{}, len(opts.Addresses))
	addresses := make([]string, 0, len(opts.Addresses))

	for _, addr := range opts.Addresses {
		addrStr := string(addr)
		// Normalize for internal matching (lowercase, no 0x)
		normalized := utils.Normalize(addrStr)
		if normalized == "" {
			continue
		}
		if _, exists := addressSet[normalized]; exists {
			continue
		}
		addressSet[normalized] = struct{}{}

		// For RPC filter, always use "0x" + normalized (lowercase with 0x prefix)
		addresses = append(addresses, "0x"+normalized)
	}

	// If no addresses, set to nil (RPC will ignore it due to omitempty)
	if len(addresses) == 0 {
		addresses = nil
	}

	// Check if fetch mode exists, fallback to logs as default if not specified
	if opts.FetchMode == "" {
		opts.FetchMode = FetchModeLogs
	}

	// Check if retryconfig exists, use default if not specified
	if opts.RetryConfig == nil {
		defaultCfg := rpc.DefaultRetryConfig()
		opts.RetryConfig = &defaultCfg
	}

	chainState := &chainState{
		chainInfo:          chain,
		opts:               opts,
		cursor:             cursor,
		blockHashCache:     NewBlockHashCache(int(cap)),
		hardFallbackBlocks: uint64(DefaultHardFallbackBlocks),
		topics:             topics,
		addresses:          addresses,
		addressSet:         addressSet,
		progress:           NewChainProgress(cursor.BlockNum),
	}

	chainState.router = router
	p.chains[chain.ChainId] = chainState

	return nil
}

// return the read-only channel
// func (p *Processor) Logs(chainId string) (<-chan types.Log, error) {
// 	p.mu.RLock()
// 	defer p.mu.RUnlock()

// 	ch, exists := p.logsCh[chainId]
// 	if !exists {
// 		return nil, fmt.Errorf("chain %s not found", chainId)
// 	}
// 	return ch, nil
// }

func (p *Processor) IsLive(chainId string) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, exists := p.chains[chainId]
	if !exists {
		return false, fmt.Errorf("chain %s not found", chainId)
	}
	return p.chains[chainId].isLive.Load(), nil
}

func (p *Processor) runChain(ctx context.Context, chain *chainState) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	chain.running.Store(true)
	defer chain.running.Store(false)
	// Check chain cursor during resume
	if err := p.checkCursorOnResume(ctx, chain); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		p.setChainError(chain, err)
		return err
	}

	// Progress logging ticker
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.logProgress(chain)
			}
		}
	}()

	// Main loop
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		before := chain.cursor.BlockNum
		if err := p.processBatch(ctx, chain); err != nil {

			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				p.logger.Info("context canceled, stopping chain processing")
				return nil
			}
			// Handle reorg errors specially - continue immediately
			if errors.Is(err, coreerrors.ErrReorgDetected) {
				p.logger.Info("reorg handled, continuing from ancestor block")
				continue
			}
			p.setChainError(chain, err)
			// Non-recoverable error - stop the chain
			p.logger.Error("chain stopping due to non-recoverable error",
				slog.String("chain_id", chain.chainInfo.ChainId),
				slog.Any("error", err))
			return fmt.Errorf("chain %s stopped due to error: %w", chain.chainInfo.ChainId, err)
		}
		p.setChainError(chain, nil)
		if chain.cursor.BlockNum == before {
			timer := time.NewTimer(chain.opts.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}

}

func (p *Processor) setChainError(chain *chainState, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err == nil {
		chain.lastErr = ""
		chain.lastErrAt = time.Time{}
	} else {
		chain.lastErr = err.Error()
		chain.lastErrAt = time.Now()
	}
}

// Function to process a batch of block for every main loop
func (p *Processor) processBatch(ctx context.Context, chain *chainState) error {
	batchCtx, batchCancel := context.WithCancel(ctx)
	defer batchCancel()

	// Plan job and return jobs channel that will be consumed by fetcher
	jobs, head, err := p.planJobs(batchCtx, chain)
	if err != nil {
		return fmt.Errorf("failed to plan jobs: %w", err)
	}

	// metrics: set processor concurrency
	p.metrics.SetProcessorConcurrency(chain.chainInfo.ChainId, uint64(chain.opts.FetcherConcurrency))

	// metrics: Observe block lag
	currentBlock := chain.cursor.BlockNum
	target := head
	if target > currentBlock {
		lag := target - currentBlock
		p.metrics.ObservedBlockLag(chain.chainInfo.ChainId, lag)
	} else {
		// Caught up - lag is 0
		p.metrics.ObservedBlockLag(chain.chainInfo.ChainId, 0)
	}

	// Fetch the job that has been planned and return the results
	results, fetchCh, err := p.fetchAll(batchCtx, chain, jobs)
	if err != nil {
		return fmt.Errorf("failed to fetch block: %w", err)
	}

	// arbiter process the results in order concurrently as fetcher sends result
	arbiterCh, arbiterErr := p.arbiter(batchCtx, chain, results, head)

	// Completion and error delivery can become ready simultaneously. Always
	// join both stages and inspect their errors before declaring success.
	var fetchErr error
	select {
	case err := <-arbiterErr:
		batchCancel()
		<-fetchCh
		<-arbiterCh
		chain.progress.ResetLogWindow()
		return err
	case fetchErr = <-fetchCh:
		if fetchErr != nil {
			batchCancel()
		}
		<-arbiterCh
	case <-arbiterCh:
		// An arbiter failure may leave fetchers blocked sending results.
		select {
		case err := <-arbiterErr:
			batchCancel()
			<-fetchCh
			return err
		default:
		}
		fetchErr = <-fetchCh
	case <-batchCtx.Done():
		<-fetchCh
		<-arbiterCh
		return batchCtx.Err()
	}
	select {
	case err := <-arbiterErr:
		return err
	default:
	}
	return fetchErr

}

// Function to check cursor on resume
func (p *Processor) checkCursorOnResume(ctx context.Context, chain *chainState) error {
	if chain.cursor.BlockNum > 0 && chain.cursor.BlockHash != "" {
		ctx, cancel := context.WithCancel(ctx)

		err := rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
			var b types.Block
			var err error
			blockNum := utils.Uint64ToHexQty(chain.cursor.BlockNum)

			reqCtx, reqCancel := context.WithTimeout(ctx, chain.opts.RetryConfig.PerRequestTimeout)
			defer reqCancel()
			b, err = chain.chainInfo.RPC.GetBlock(reqCtx, blockNum)
			if err != nil {
				return err
			}

			if b.Hash != chain.cursor.BlockHash {
				p.logger.Warn("cursor hash mismatch, handling reorg",
					slog.String("chain_id", chain.chainInfo.ChainId),
					slog.String("expected", chain.cursor.BlockHash),
					slog.String("actual", b.Hash))

				ancestor, hash := p.handleReorg(ctx, chain)

				rollBackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := p.sink.Rollback(rollBackCtx, chain.chainInfo.ChainId, ancestor+1, hash); err != nil {
					return fmt.Errorf("rollback failed after startup reorg at block %d: %w", ancestor, err)
				}
				p.mu.Lock()
				chain.cursor.BlockNum = ancestor
				chain.cursor.BlockHash = hash
				chain.progress.Rollback(ancestor, time.Now())
				p.mu.Unlock()
			}
			return nil
		})
		cancel()

		return err
	}
	return nil
}

func (p *Processor) getBlockWithRetry(ctx context.Context, blockNum uint64, chain *chainState) (types.Block, error) {
	var block types.Block
	var err error

	err = rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
		reqCtx, cancel := context.WithTimeout(ctx, chain.opts.RetryConfig.PerRequestTimeout)
		defer cancel()
		block, err = chain.chainInfo.RPC.GetBlock(reqCtx, utils.Uint64ToHexQty(blockNum))
		return err
	})

	return block, err
}

func (p *Processor) logProgress(chain *chainState) {
	// Take snapshot and log
	snapshot := chain.progress.Snapshot()
	status := "syncing"

	if chain.isLive.Load() {
		status = "live"
	}

	p.logger.Info(fmt.Sprintf("[%s] Block %s | %.1f%% | %.0f blk/s | ETA %s | %s events",
		chain.chainInfo.ChainId,
		utils.FormatNumber(snapshot.current),
		snapshot.progressPct,
		snapshot.blockPerSec,
		snapshot.eta,
		utils.FormatNumber(snapshot.events),
	), slog.String("status", status))

	// Reset window for next calculation
	chain.progress.ResetLogWindow()
}
