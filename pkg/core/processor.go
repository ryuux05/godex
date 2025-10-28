package core

import (
	"context"
	"fmt"
	"log"
	"sync"

	"golang.org/x/sync/errgroup"
)

type chainState struct {
	// chainInfo stores chain information where the indexer going to query
	// Specify RPC (endpoint and rate-limit) 
	chainInfo ChainInfo
	// cursor is a pointer that points the current block where the indexer is pointing 
	cursor uint64
	
	// FIFO of endHeights in commit order
	windowOrder []uint64
	// Store block hash to compare the next block parent hash.
	// We need this in order to detect reorg happening.
	storedWindowHash map[uint64]string
	// Number that bound how many hash could be store in storedWindowHash
	storedWindowHashCap uint64
	// The number of block that we will fall back to in case we couldnt resolve reorg
	hardFallbackBlocks uint64
	// Storage to store the formatted topics
	topics []string
	// options for processor
	opts *Options
}

type Processor struct {
	// chains is an internal per-chain state
	// It's a map with chainId as key.
	chains map[string]*chainState
	// logsChan is a channel where processor will store the indexed logs
	// It's a map with chainId as key.
	logsCh map[string]chan Log
	// isRunning track the processor state if it's running or stopped.
	// False by default until the processor run.
	isRunning bool
	// Mutex to access data safely
	mu sync.RWMutex
}

func NewProcessor() *Processor {
	return &Processor{
		chains: make(map[string]*chainState),
		logsCh: make(map[string]chan Log),
		isRunning: false,
	}
}


func (p *Processor) AddChain(chain ChainInfo, opts *Options) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if p.isRunning {
        return fmt.Errorf("cannot add chain while processor is running")
    }

	cursor := opts.StartBlock; if cursor == 0 { cursor = 0 }

	// Clamp the max storedwindowhash bound.
	rs := uint64(opts.RangeSize)         // assume >0
	base := (opts.ReorgLookbackBlocks + rs - 1) / rs // ceil
	cap := base + 1
	if cap < 8 { cap = 8 }
	if cap > 256 { cap = 256 }

	topics := ConvertToTopics(opts.Topics)

	// Check if fetch mode exists, fallback to logs as default if not specified
	if opts.FetchMode == "" {
		opts.FetchMode = FetchModeLogs
	}

	// Check if retryconfig exists, use default if not specified
	if opts.RetryConfig == nil {
		defaultCfg := DefaultRetryConfig()
    	opts.RetryConfig = &defaultCfg
	}

	chainState := &chainState{
		chainInfo: chain,
		opts: opts,
		cursor: cursor,
		storedWindowHashCap: cap,
		storedWindowHash: make(map[uint64]string, cap),
		hardFallbackBlocks: 1000,
		topics: topics,
	}

	p.chains[chain.ChainId] = chainState
	p.logsCh[chain.ChainId] = make(chan Log, opts.LogsBufferSize)

	return nil
}

func (p *Processor) GetChain(chainId string) ChainInfo {
	return p.chains[chainId].chainInfo
}

func (p *Processor) Run(ctx context.Context) error{
	p.isRunning = true
    defer func() { p.isRunning = false }()

	g := errgroup.Group{}
	for chainId, chain := range p.chains {
		id := chainId
        c := chain
		ch := p.logsCh[id]
        
		g.Go(func () error  {	
			err := p.runChain(ctx, ch, c)
			if err != nil {
                log.Printf("Chain %s stopped: %v", id, err)
                // Error logged but doesn't stop other chains
            }
            return err  
		})

	}

	return g.Wait()
}

// return the read-only channel
func (p *Processor) Logs(chainId string) (<-chan Log, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	ch, exists := p.logsCh[chainId]
    if !exists {
        return nil, fmt.Errorf("chain %s not found", chainId)
    }
    return ch, nil
}

func (p *Processor) runChain(ctx context.Context, logsCh chan Log, chain *chainState) error {
outer:
	for {		
		rpcCtx, rpcCancel := context.WithCancel(ctx)

		// compute for new head
		var headHex string
		err := RetryWithBackoff(rpcCtx, *chain.opts.RetryConfig, func() error {
			var err error
			headHex, err = chain.chainInfo.RPC.Head(rpcCtx)
			return err
		})
		if err != nil {
			rpcCancel()
			return err
		}

		head, err := HexQtyToUint64(headHex)
		if err != nil {
			log.Println("Error in converting hex to uint64", err)
			rpcCancel()
			return err
		}

		// look for block confimation
		var conf uint64
		if chain.opts.Confimation > 0 {
			conf = chain.opts.Confimation
		}

		// Get the target block
		target := uint64(0)
		if head > conf {
			target = head - conf
		}

		n := chain.opts.FetcherConcurrency
		if n <= 0 {
			n = 1
		}
		
		// plan jobs
		type blockRange struct {
			from uint64
			to uint64
		}
		jobs := make(chan blockRange ,n)
		go func() {
			defer close(jobs)
			rs := uint64(chain.opts.RangeSize)

			for from := chain.cursor + 1; from <= target; from += rs {
				to := from + rs - 1
				if to > target {
					to = target
				}

				select {
				case <-rpcCtx.Done():
					return
				case jobs <- blockRange{from, to}:
				//log.Printf("planned job from block %d to block %d...\n", from, to)
				}
			} 
		}()	
				
		// create waitgroup and make error channel
		var wg sync.WaitGroup
		wg.Add(n)
		errCh := make(chan error, 1)

		type doneMsg struct {
			from uint64
			to uint64
			logs []Log
		}
		
		doneCh := make(chan doneMsg, n)

		for i := 0; i < n; i++ {
			go func(){
				defer wg.Done()
				for job := range jobs {
					var logs []Log
					var err error
					err = RetryWithBackoff(rpcCtx, *chain.opts.RetryConfig, func() error {	
						switch chain.opts.FetchMode {
						case FetchModeLogs:
							filter := Filter{
								FromBlock: Uint64ToHexQty(job.from),
								ToBlock: Uint64ToHexQty(job.to),
								Topics: chain.topics,
							}
							logs, err = chain.chainInfo.RPC.GetLogs(rpcCtx, filter)

						case FetchModeReceipts:
							logs, err = p.fetchLogsFromReceipts(rpcCtx, job.from, job.to, chain)
						}

						return err
					})
						if err != nil {
							log.Println("Error fetching logs: ", err)
							select {
							case errCh <- err:
								return
							default:
								return
							}
						}
						//log.Printf("Here")
						select {
							case <-rpcCtx.Done():
								return
							case doneCh <- doneMsg{from: job.from, to: job.to, logs: logs}:
								//log.Printf("sending log to arbiter from block %d to block %d...\n", job.from, job.to)
						}
			
				}
			}()
		}

		// close logs when fetchers finished, or early retry
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(doneCh)
			close(done)
		}()

		// goroutine to check job windows and cursor strategy
		arbiterDone := make(chan struct{})
		go func() {
			defer close(arbiterDone)
			window := make(map[uint64]uint64)
			windowLogs:= make(map[uint64][]Log)
			next := chain.cursor + 1

			for {
				select {
				case <-rpcCtx.Done():
					return
				case dm, ok := <-doneCh:
					if !ok {return};
					
					window[dm.from] = dm.to
					windowLogs[dm.from] = dm.logs

					for end, ok2 := window[next]; ok2; end, ok2 = window[next] {
						
						// Get start window blockhash and compare it with the stored blockhash
						var block Block
						err := RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
							var err error
							block, err = chain.chainInfo.RPC.GetBlock(rpcCtx, Uint64ToHexQty(next))
							return err
						})

						if err != nil {
							if rpcCtx.Err() != nil { 
								return 
							} else {    
								select { 
									case errCh <- err: 
									default: 
								} 
								return
							}
						}

						
						if next == 0 {
							break
						}
						
						//Compare to parents
						parent, ok := chain.storedWindowHash[next - 1]
						if (ok && block.ParentHash != parent) {
							log.Println("Hash mismatch, reorg happened...")
							rpcCancel()
							ancestor := p.handleReorg(ctx, chain)

							chain.cursor = ancestor
							return

						} else {
							log.Printf("Processed log from block %d to block %d...\n", next, end)
							// Commit logs to log channel
							if logs := windowLogs[next]; len(logs) > 0 {
								for _, l:= range logs {
								select {
								case <-rpcCtx.Done():
									return
								case logsCh <- l:
								}
								}
							}
							
							delete(windowLogs, next)
							delete(window, next)	
							chain.cursor = end
							next = end + 1
						}
						
						// Get the end block blockhash after committing
						err = RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
							var err error
							block, err = chain.chainInfo.RPC.GetBlock(rpcCtx, Uint64ToHexQty(end))
							return err
						})
						if err != nil {
							if rpcCtx.Err() != nil { return }        // batch was canceled; ignore
							log.Println("Error getting window end block: ", err)
							select { case errCh <- err: default: }
							return
						}

						p.storeWindowHash(end, block.Hash, chain)
					}
				}
			}
		}()
		
		// listen to condition channel
		for{
			select {
			case <-rpcCtx.Done():
				<- done
				<- arbiterDone
				continue outer
			case <-done:
				<- arbiterDone
				continue outer
			case err := <-errCh:
				log.Println("Error received cancelling context")
				rpcCancel()
				<-done
				<- arbiterDone
				return err
			case <- ctx.Done():
				rpcCancel()
				<- done
				<- arbiterDone
				return nil
			}

		}
	}
}

// During ancestor lookup we start from the cursor window and get to the window head and compare to the previous window
func (p *Processor) handleReorg(ctx context.Context, chain *chainState) uint64 {
	ancestor := chain.cursor
	for i := uint64(0); i < chain.storedWindowHashCap; i++ {

		fallback := chain.cursor; if fallback > chain.hardFallbackBlocks { fallback -= chain.hardFallbackBlocks } else { fallback = 0 }

		windowHeadBlock, err := chain.chainInfo.RPC.GetBlock(ctx, Uint64ToHexQty(ancestor + 1))
		if err != nil {
			return fallback
		}
		
		if windowHeadBlock.ParentHash == chain.storedWindowHash[ancestor] {
			p.dropWindowHash(ancestor, chain)
			log.Println("Found ancestor: ", ancestor)
			return ancestor
		}
		
		if ancestor < uint64(chain.opts.RangeSize) {
			ancestor = 0
			break
		}
		ancestor -= uint64(chain.opts.RangeSize)

		select{
		case<- ctx.Done():
			return fallback
		default:
		}
	}
	fallback := chain.cursor; if fallback > chain.hardFallbackBlocks { fallback -= chain.hardFallbackBlocks } else { fallback = 0 }
	log.Println("Hard fallback triggered...")
	if fallback <= 0 {
		fallback = 0
	}
	p.dropWindowHash(fallback, chain)
	return fallback
}

func (p *Processor) storeWindowHash(to uint64, blockHash string, chain *chainState) {
	_, exist := chain.storedWindowHash[to]
	if exist {
		chain.storedWindowHash[to] = blockHash
	}else {
		l := len(chain.windowOrder)
		if uint64(l) >= chain.storedWindowHashCap {
			old := chain.windowOrder[0]
			delete(chain.storedWindowHash, old)
			chain.windowOrder = chain.windowOrder[1:]
		}

		chain.storedWindowHash[to] = blockHash
		chain.windowOrder = append(chain.windowOrder, to)
	}
}

func (p *Processor) dropWindowHash(after uint64, chain *chainState) {
		// walk tail backward removing entries > after
		i := len(chain.windowOrder) - 1
		for i >= 0 && chain.windowOrder[i] > after {
			delete(chain.storedWindowHash, chain.windowOrder[i])
			i--
		}

		chain.windowOrder = chain.windowOrder[:i+1]
}

// Helper function to get logs from receipts
func(p *Processor) fetchLogsFromReceipts(ctx context.Context, from uint64, to uint64, chain *chainState) ([]Log, error){
	var allLogs []Log
	for blockNum := from; blockNum <= to; blockNum ++ {
		s_blockNum := Uint64ToHexQty(blockNum)
		receipts, err := chain.chainInfo.RPC.GetBlockReceipts(ctx, s_blockNum)
		if err != nil {
			return nil, fmt.Errorf("failed to get receipts for block %d: %w", blockNum, err)
		}

		for _, receipt := range receipts {
			for _, log := range receipt.Logs {
				if p.matchesTopicFilter(log, chain) {
                    allLogs = append(allLogs, log)
                }
			}
		}
	}
	return allLogs, nil
}

// Checks if a log matches the configurated topic
func(p *Processor) matchesTopicFilter(log Log, chain *chainState) bool {
	// If there is no topic specified then its true by default
	if len(chain.opts.Topics) == 0 {
		return true
	}

	// Check if log has enough topics
    if len(log.Topics) == 0 {
        return false
    }

	// Match first topic (event signature)
    for _, filterTopic := range chain.topics {
        if len(log.Topics) > 0 {
            if logTopic, ok := log.Topics[0].(string); ok {
                if logTopic == filterTopic {
                    return true
                }
            }
        }
    }
    
    return false
}





