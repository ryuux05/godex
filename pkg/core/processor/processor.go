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
		router:  nil,
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
	p.isRunning = true
	defer func() { p.isRunning = false }()

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
	addresses := make([]string, len(opts.Addresses))

	for _, addr := range opts.Addresses {
		addressSet[string(addr)] = struct{}{}
		addresses = append(addresses, string(addr))
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
		hardFallbackBlocks: 1000,
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

	// Main loop
	for {
		select {
		case <- ctx.Done():
			return nil
		default:
		}

		if err := p.processBatch(ctx, chain); err != nil {
			if err == context.Canceled {
				p.logger.Info("context canceled, stopping chain processing")
				return nil
			}
			 // Handle reorg errors specially - continue immediately
    		if errors.Is(err, coreerrors.ErrReorgDetected) {
        		p.logger.Info("reorg handled, continuing from ancestor block")
        	continue
    		}
			
			p.logger.Error("batch failed", slog.Any("error", err))
			time.Sleep(5 * time.Second)
			chain.progress.ResetLogWindow()
		}
	}
	
}

// func (p *Processor) runChain(ctx context.Context, chain *chainState) error {
// 	// Check chain cursor during resume
// 	if err := p.checkCursorOnResume(ctx, chain); err != nil {
// 		return err
// 	}

// 	// Main loop
// 	for {
// 		rpcCtx, rpcCancel := context.WithCancel(ctx)
// 		// compute for new head
// 		var headHex string
// 		err := rpc.RetryWithBackoff(rpcCtx, *chain.opts.RetryConfig, func() error {
// 			var err error
// 			headHex, err = chain.chainInfo.RPC.Head(rpcCtx)
// 			return err
// 		})
// 		if err != nil {
// 			rpcCancel()
// 			return err
// 		}

// 		head, err := utils.HexQtyToUint64(headHex)
// 		if err != nil {
// 			p.logger.Error("failed to convert hex to uint64", slog.Any("error", err))
// 			rpcCancel()
// 			return err
// 		}
// 		chain.progress.SetHead(head)

// 		// look for block confimation
// 		var conf uint64
// 		if chain.opts.ConfirmationDepth > 0 {
// 			conf = chain.opts.ConfirmationDepth
// 		}

// 		// Get the target block
// 		target := uint64(0)
// 		if head > conf {
// 			target = head - conf
// 		}

// 		fetchWorker := chain.opts.FetcherConcurrency
// 		if fetchWorker <= 0 {
// 			fetchWorker = 1
// 		}

// 		// Check for live sync
// 		if chain.cursor.BlockNum >= head-chain.opts.ConfirmationDepth {
// 			chain.isLive = true
// 		}

// 		// Also when blocknum exceed head we need to bring it back to confirmation level
// 		if chain.cursor.BlockNum > head {
// 			chain.cursor.BlockNum = head - chain.opts.ConfirmationDepth
// 		}

// 		// Metrics to measure processor concurrency count.
// 		p.metrics.SetProcessorConcurrency(chain.chainInfo.ChainId, uint64(fetchWorker))

// 		// plan jobs
// 		type blockRange struct {
// 			from uint64
// 			to   uint64
// 		}
// 		jobs := make(chan blockRange, fetchWorker)
// 		go func() {
// 			defer close(jobs)
// 			rs := uint64(chain.opts.RangeSize)

// 			for from := chain.cursor.BlockNum + 1; from <= target; from += rs {
// 				to := from + rs - 1
// 				if to > target {
// 					to = target
// 				}

// 				select {
// 				case <-rpcCtx.Done():
// 					return
// 				case jobs <- blockRange{from, to}:
// 					//log.Printf("planned job from block %d to block %d...\n", from, to)
// 				}
// 			}
// 		}()

// 		// create waitgroup and make error channel
// 		var wg sync.WaitGroup
// 		wg.Add(fetchWorker)
// 		errCh := make(chan error, 1)

// 		doneCh := make(chan doneMsg, fetchWorker)

// 		for i := 0; i < fetchWorker; i++ {
// 			go func() {
// 				defer wg.Done()
// 				for job := range jobs {
// 					var logs []types.Log
// 					var err error
// 					start := time.Now()

// 					mode := chain.opts.FetchMode

// 					if !chain.isLive && chain.opts.UseLogsForHistoricalSync {
// 						mode = FetchModeLogs
// 					}

// 					// When the chain is live
// 					err = rpc.RetryWithBackoff(rpcCtx, *chain.opts.RetryConfig, func() error {
// 						switch mode {
// 						case FetchModeLogs:
// 							filter := types.Filter{
// 								FromBlock: utils.Uint64ToHexQty(job.from),
// 								ToBlock:   utils.Uint64ToHexQty(job.to),
// 								Topics:    chain.topics,
// 								Address:   chain.addresses,
// 							}

// 							// Record fetch time
// 							logs, err = chain.chainInfo.RPC.GetLogs(rpcCtx, filter)

// 						case FetchModeReceipts:
// 							logs, err = p.fetchLogsFromReceipts(rpcCtx, job.from, job.to, chain)

// 						}

// 						return err
// 					})

// 					p.metrics.ObservedBlockFetchDuration(chain.chainInfo.ChainId, time.Since(start), err == nil)
// 					if err != nil {
// 						p.logger.Error("failed to fetch logs", slog.String("chain_id", chain.chainInfo.ChainId), slog.Any("error", err))
// 						select {
// 						case errCh <- err:
// 							return
// 						default:
// 							return
// 						}
// 					}

// 					// Fetch timestamps if enabled
// 					var timestamps map[uint64]uint64
// 					if chain.opts.EnableTimestamps && len(logs) > 0 {
// 						// Collect unique block numbers
// 						uniqueBlocks := make(map[uint64]struct{})
// 						for _, l := range logs {
// 							bn, err := utils.HexQtyToUint64(l.BlockNumber)
// 							if err != nil {
// 								p.logger.Warn("failed to parse block number", slog.String("block_number", l.BlockNumber), slog.Any("error", err))
// 								continue
// 							}
// 							uniqueBlocks[bn] = struct{}{}
// 						}

// 						// Convert to slice for batch request
// 						blockNumbers := make([]string, 0, len(uniqueBlocks))
// 						for bn := range uniqueBlocks {
// 							blockNumbers = append(blockNumbers, utils.Uint64ToHexQty(bn))
// 						}

// 						// Try batch fetch first, fall back to individual calls
// 						timestamps = make(map[uint64]uint64)
// 						blocks, err := chain.chainInfo.RPC.GetBlocks(rpcCtx, blockNumbers)
// 						if err != nil {
// 							p.logger.Warn("batch GetBlocks failed, falling back to individual calls", slog.Any("error", err))
// 							// Fall back to individual GetBlock calls
// 							for _, hexBn := range blockNumbers {
// 								bn, err := utils.HexQtyToUint64(hexBn)
// 								if err != nil {
// 									p.logger.Warn("failed to parse block number", slog.String("block_number", hexBn), slog.Any("error", err))
// 									continue
// 								}
// 								block, err := chain.chainInfo.RPC.GetBlock(rpcCtx, hexBn)
// 								if err != nil {
// 									p.logger.Warn("failed to fetch block", slog.String("block_number", hexBn), slog.Any("error", err))
// 									continue
// 								}
// 								ts, err := utils.HexQtyToUint64(block.Timestamp)
// 								if err != nil {
// 									p.logger.Warn("failed to parse timestamp", slog.String("block_number", hexBn), slog.Any("error", err))
// 									continue
// 								}
// 								timestamps[bn] = ts
// 							}
// 						} else {
// 							// Process batch results
// 							for hexBn, blk := range blocks {
// 								bn, err := utils.HexQtyToUint64(hexBn)
// 								if err != nil {
// 									p.logger.Warn("failed to parse block number", slog.String("block_number", hexBn), slog.Any("error", err))
// 									continue
// 								}
// 								ts, err := utils.HexQtyToUint64(blk.Timestamp)
// 								if err != nil {
// 									p.logger.Warn("failed to parse timestamp", slog.Uint64("block_number", bn), slog.Any("error", err))
// 									continue
// 								}
// 								timestamps[bn] = ts
// 							}
// 						}
// 					}

// 					select {
// 					case <-rpcCtx.Done():
// 						return
// 					case doneCh <- doneMsg{from: job.from, to: job.to, logs: logs, timestamps: timestamps}:
// 					}
// 				}

// 			}()
// 		}

// 		// close logs when fetchers finished, or early retry
// 		done := make(chan struct{})
// 		go func() {
// 			wg.Wait()
// 			//close(doneCh)
// 			close(done)
// 		}()

// 		// goroutine to check job windows and cursor strategy
// 		arbiterDone := make(chan struct{})
// 		go func() {
// 			defer func() {
// 				if r := recover(); r != nil {
// 					p.logger.Error("ARBITER PANICKED", slog.Any("panic", r))
// 				}
// 				p.logger.Info("ARBITER EXITING") // ADD - see when it exits
// 				close(arbiterDone)
// 			}()

// 			window := make(map[uint64]uint64)
// 			windowLogs := make(map[uint64][]types.Log)
// 			windowTimestamps := make(map[uint64]map[uint64]uint64) // from -> (blockNum -> timestamp)
// 			next := chain.cursor.BlockNum + 1

// 			// Progress logging ticker
// 			progressTicker := time.NewTicker(30 * time.Second)
// 			defer progressTicker.Stop()
// 			for {
// 				select {
// 				case <-rpcCtx.Done():
// 					p.logger.Debug("arbiter exit: rpcCtx cancelled")
// 					return
// 				case <-progressTicker.C:
// 					// Take snapshot and log
// 					snapshot := chain.progress.Snapshot()
// 					status := "syncing"
// 					if chain.isLive {
// 						status = "live"
// 					}
// 					p.logger.Info(fmt.Sprintf("[%s] Block %s | %.1f%% | %.0f blk/s | ETA %s | %s events",
// 						chain.chainInfo.ChainId,
// 						utils.FormatNumber(snapshot.current),
// 						snapshot.progressPct,
// 						snapshot.blockPerSec,
// 						snapshot.eta,
// 						utils.FormatNumber(snapshot.events),
// 					), slog.String("status", status))

// 					// Reset window for next calculation
// 					chain.progress.ResetLogWindow()
// 				case dm, ok := <-doneCh:
// 					if !ok {
// 						p.logger.Debug("arbiter exit: doneCh closed")
// 						return
// 					}

// 					window[dm.from] = dm.to
// 					windowLogs[dm.from] = dm.logs
// 					if dm.timestamps != nil {
// 						windowTimestamps[dm.from] = dm.timestamps
// 					}

// 					for end, ok2 := window[next]; ok2; end, ok2 = window[next] {

// 						// Get start window blockhash and compare it with the stored blockhash
// 						var block types.Block
// 						err := rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
// 							var err error
// 							block, err = chain.chainInfo.RPC.GetBlock(rpcCtx, utils.Uint64ToHexQty(next))
// 							return err
// 						})

// 						if err != nil {
// 							if rpcCtx.Err() != nil {
// 								return
// 							} else {
// 								select {
// 								case errCh <- err:
// 								default:
// 								}
// 								return
// 							}
// 						}

// 						if next == 0 {
// 							break
// 						}

// 						//Compare to parents
// 						parent, ok := chain.blockHashCache.Get(next - 1)
// 						if ok && block.ParentHash != parent {
// 							p.logger.Warn("hash mismatch, reorg detected", slog.String("chain_id", chain.chainInfo.ChainId), slog.Uint64("block", next))
// 							rpcCancel()

// 							// Metrics to measure reorgs
// 							p.metrics.IncReorgs(chain.chainInfo.ChainId)

// 							ancestor := p.handleReorg(ctx, chain)

// 							// Rollback sink to ancestor
// 							ancestorHash, exist := chain.blockHashCache.Get(ancestor)
// 							if !exist {
// 								block, err := chain.chainInfo.RPC.GetBlock(rpcCtx, utils.Uint64ToHexQty(ancestor))
// 								if err == nil {
// 									ancestorHash = block.Hash
// 								}
// 							}
// 							if err := p.sink.Rollback(rpcCtx, chain.chainInfo.ChainId, ancestor, ancestorHash); err != nil {
// 								p.logger.Error("failed to rollback sink", slog.String("chain_id", chain.chainInfo.ChainId), slog.Any("error", err))
// 					}

// 							chain.cursor.BlockNum = ancestor
// 							chain.cursor.BlockHash = ancestorHash
// 							p.logger.Debug("arbiter exit: reorg rollback")
// 							return

// 						} else {
// 							p.logger.Debug("processed block range", slog.String("chain_id", chain.chainInfo.ChainId), slog.Uint64("from", next), slog.Uint64("to", end))
// 							// Decode logs to events and store to sink
// 							if logs := windowLogs[next]; len(logs) > 0 {
// 								events := make([]types.Event, 0, len(logs))
// 								timestamps := windowTimestamps[next] // Get timestamps for this window

// 								dec := p.decoder[chain.chainInfo.ChainId]
// 								for _, l := range logs {
// 									p.logger.Debug("attempting decode",
// 										slog.String("address", l.Address),
// 										slog.String("topic0", l.Topics[0]))
// 									event, err := dec.Decode(chain.chainInfo.Name, chain.chainInfo.ChainId, l)
// 									if err != nil {
// 										p.logger.Warn("failed to decode log", slog.String("chain_id", chain.chainInfo.ChainId), slog.Any("error", err))
// 										continue
// 									}
// 									if event != nil {
// 										// Apply timestamp if enabled and available
// 										if chain.opts.EnableTimestamps && timestamps != nil {
// 											bn, err := utils.HexQtyToUint64(l.BlockNumber)
// 											if err != nil {
// 												p.logger.Warn("failed to parse block number for timestamp", slog.Any("error", err))
// 											} else {
// 												event.Timestamp = timestamps[bn]
// 											}
// 										}
// 										events = append(events, *event)
// 									}
// 								}

// 								// Store events to sink
// 								if len(events) > 0 {
// 									if err := p.sink.Store(rpcCtx, events); err != nil {
// 										p.logger.Debug("arbiter exit: store failed")
// 										p.logger.Error("failed to store events", slog.String("chain_id", chain.chainInfo.ChainId), slog.Any("error", err))
// 										select {
// 										case errCh <- err:
// 										default:
// 										}
// 										return
// 									}
// 								}
// 								//update progress
// 								chain.progress.Update(end, chain.progress.eventsStored+uint64(len(events)))
// 							}

// 							delete(windowLogs, next)
// 							delete(windowTimestamps, next)
// 							delete(window, next)
// 							chain.cursor.BlockNum = end

// 							next = end + 1

// 							lag := head - chain.cursor.BlockNum
// 							p.metrics.ObservedBlockLag(chain.chainInfo.ChainId, lag)

// 						}

// 						// Get the end block blockhash after committing
// 						err = rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
// 							var err error
// 							block, err = chain.chainInfo.RPC.GetBlock(rpcCtx, utils.Uint64ToHexQty(end))
// 							return err
// 						})
// 						if err != nil {
// 							if rpcCtx.Err() != nil {
// 								return
// 							} // batch was canceled; ignore
// 							p.logger.Error("failed to get window end block", slog.String("chain_id", chain.chainInfo.ChainId), slog.Any("error", err))
// 							select {
// 							case errCh <- err:
// 							default:
// 							}
// 							return
// 						}

// 						chain.blockHashCache.Set(end, block.Hash)
// 					}
// 				}
// 			}
// 		}()

// 		// listen to condition channel
// 		for {
// 			select {
// 			case <-rpcCtx.Done():
// 				p.logger.Debug("arbiter exit: rpcCtx cancelled")
// 				<-done
// 				<-arbiterDone
// 				continue outer
// 			case <-done:
// 				<-arbiterDone
// 				continue outer
// 			case err := <-errCh:
// 				p.logger.Error("error received, cancelling context", slog.Any("error", err))
// 				rpcCancel()
// 				<-done
// 				<-arbiterDone

// 				// Retry the batch when error received
// 				p.logger.Warn("restarting batch after error")
// 				time.Sleep(5 * time.Second)

// 				// Reset chain progress
// 				chain.progress.ResetLogWindow()
// 				continue outer

// 			case <-ctx.Done():
// 				rpcCancel()
// 				<-done
// 				<-arbiterDone
// 				return nil
// 			}

// 		}
// 	}
// }

// Function to process a batch of block for every main loop
func (p *Processor) processBatch(ctx context.Context, chain *chainState) error {
	batchCtx, batchCancel := context.WithCancel(ctx)
	defer batchCancel()

	// Plan job and return jobs channel that will be consumed by fetcher
	jobs, head, err := p.planJobs(batchCtx, chain)
	if err != nil {
		return fmt.Errorf("failed to plan jobs: %w", err)
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
	case err := <- arbiterErr:

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
		<- fetchCh
		// arbiter failed- wait for arbiter channel to close
		<- arbiterCh
		chain.progress.ResetLogWindow()
		return err

	// case where fetch done early, we will wait for arbiter
	case <-fetchCh:
		<- arbiterCh
		return nil

	case <- batchCtx.Done():
		// Context canceled - wait for both to complete gracefully
		<- fetchCh
		<- arbiterCh
		return batchCtx.Err()
	
	case <- arbiterCh:
		<- fetchCh
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

				if err := p.sink.Rollback(ctx, chain.chainInfo.ChainId, ancestor, hash); err != nil {
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
