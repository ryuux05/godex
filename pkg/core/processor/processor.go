package processor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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
	isLive bool
	// options for processor
	opts *Options
	//chain Progress
	progress *chainProgress
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
	// Mutex to access data safely
	mu sync.RWMutex
	// metrics
	metrics metrics.Metrics
	// Sink is a persistance storage
	sink sink.Sink
	// Router is used to store decoding condition and the respectitive decoder
	// Decoder is used to decode log to a human readable event
	// Each chain are able to to have different decoder
	router *decoder.DecoderRouter
	// logger is for strucutured logging
	logger *slog.Logger
}

func NewProcessor(m metrics.Metrics, s sink.Sink) *Processor {
	if m == nil {
		m = metrics.Noop{}
	}
	return &Processor{
		chains: make(map[string]*chainState),
		//logsCh:    make(map[string]chan types.Log),
		metrics:   m,
		sink:      s,
		router:    nil,
		logger:    slog.Default(),
		isRunning: false,
	}
}

func (p *Processor) AddChain(chain ChainInfo, opts *Options, router *decoder.DecoderRouter) error {
	blockNum, blockHash, err := p.sink.LoadCursor(context.Background(), chain.ChainId)
	p.logger.Info("cursor loaded from sink",
		slog.String("chain_id", chain.ChainId),
		slog.Uint64("loaded_block_num", blockNum),
		slog.String("loaded_block_hash", blockHash),
		slog.Uint64("start_block", opts.StartBlock),
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

	return p.addChain(chain, opts, blockNum, blockHash, router)
}

func (p *Processor) SetLogger(l *slog.Logger) {
	p.logger = l
}

func (p *Processor) GetChain(chainId string) ChainInfo {
	return p.chains[chainId].chainInfo
}

func (p *Processor) Run(ctx context.Context) error {
	p.mu.Lock()
	p.isRunning = true
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

	startBlock := opts.StartBlock
	if startBlock <= 0 {
		startBlock = 0
	}

	var cursor *cursorState = &cursorState{}
	if blockNum > 0 {

		if blockNum > startBlock {
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
	rs := uint64(opts.RangeSize)                     // assume >0
	base := (opts.ReorgLookbackBlocks + rs - 1) / rs // ceil
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

	p.chains[chain.ChainId] = chainState
	//p.logsCh[chain.ChainId] = make(chan types.Log, opts.LogsBufferSize)
	p.router = router

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
	_, exists := p.chains[chainId]
	if !exists {
		return false, fmt.Errorf("chain %s not found", chainId)
	}
	return p.chains[chainId].isLive, nil
}

func (p *Processor) runChain(ctx context.Context, chain *chainState) error {
	// Check chain cursor during resume
	if err := p.checkCursorOnResume(ctx, chain); err != nil {
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

		if err := p.processBatch(ctx, chain); err != nil {
			// store err and the time it occured
			chain.lastErr = err.Error()
			chain.lastErrAt = time.Now()

			if err == context.Canceled {
				p.logger.Info("context canceled, stopping chain processing")
				return nil
			}
			// Handle reorg errors specially - continue immediately
			if errors.Is(err, coreerrors.ErrReorgDetected) {
				p.logger.Info("reorg handled, continuing from ancestor block")
				continue
			}
			// Non-recoverable error - stop the chain
			p.logger.Error("chain stopping due to non-recoverable error",
				slog.String("chain_id", chain.chainInfo.ChainId),
				slog.Any("error", err))
			return fmt.Errorf("chain %s stopped due to error: %w", chain.chainInfo.ChainId, err)
		}
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
	target := head - chain.opts.ConfirmationDepth
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

	select {
	// case where there is error in arbiter
	case err := <-arbiterErr:

		if errors.Is(err, coreerrors.ErrReorgDetected) {
			var reorgErr *coreerrors.ReorgError
			if errors.As(err, &reorgErr) {
				p.logger.Info("reorg detected",
					slog.Uint64("block", reorgErr.BlockNum),
					slog.String("hash", reorgErr.BlockHash),
				)
			}
		}

		// Cancel the batch when there is error with arbiter
		batchCancel()
		// arbiter failed - still wait for fetchers to complete
		<-fetchCh
		// arbiter failed- wait for arbiter channel to close
		<-arbiterCh
		chain.progress.ResetLogWindow()
		return err

	// case where fetch done early, we will wait for arbiter
	case <-fetchCh:
		<-arbiterCh
		return nil

	case <-batchCtx.Done():
		// Context canceled - wait for both to complete gracefully
		<-fetchCh
		<-arbiterCh
		return batchCtx.Err()

	case <-arbiterCh:
		<-fetchCh
		return nil
	}
}

// Function to check cursor on resume
func (p *Processor) checkCursorOnResume(ctx context.Context, chain *chainState) error {
	if chain.cursor.BlockNum > 0 && chain.cursor.BlockHash != "" {
		ctx, cancel := context.WithCancel(ctx)

		err := rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
			var b types.Block
			var err error
			blockNum := utils.Uint64ToHexQty(chain.cursor.BlockNum)

			b, err = chain.chainInfo.RPC.GetBlock(ctx, blockNum)
			if err != nil {
				return err
			}

			if b.Hash != chain.cursor.BlockHash {
				p.logger.Warn("cursor hash mismatch, handling reorg",
					slog.String("chain_id", chain.chainInfo.ChainId),
					slog.String("expected", chain.cursor.BlockHash),
					slog.String("actual", b.Hash))

				ancestor, hash := p.handleReorg(ctx, chain)

				chain.cursor.BlockNum = ancestor
				chain.cursor.BlockHash = hash

				rollBackCtx, cancel := context.WithTimeout(context.Background(), 30 * time.Second)
				defer cancel()
				if err := p.sink.Rollback(rollBackCtx, chain.chainInfo.ChainId, ancestor, hash); err != nil {
					p.logger.Error("failed to rollback sink", slog.String("chain_id", chain.chainInfo.ChainId), slog.Any("error", err))
				}
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
		block, err = chain.chainInfo.RPC.GetBlock(ctx, utils.Uint64ToHexQty(blockNum))
		return err
	})

	return block, err
}

func (p *Processor) logProgress(chain *chainState) {
	// Take snapshot and log
	snapshot := chain.progress.Snapshot()
	status := "syncing"

	if chain.isLive {
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
