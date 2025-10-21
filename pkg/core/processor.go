package core

import (
	"context"
	"fmt"
	"log"
	"sync"
)


type Processor struct {
	// RPC instances where the indexer going to quer
	// Specify endpoint and rate-limit
	rpc RPC 
	// cursor is a pointer that points the current block where the indexer is pointing 
	cursor uint64
	// logsChan is a channel where processor will store the indexed logs
	logsCh chan Log
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

func NewProcessor(rpc RPC, opts *Options) *Processor {
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

	return &Processor{
		rpc: rpc,
		opts: opts,
		cursor: cursor,
		logsCh: make(chan Log, opts.LogsBufferSize),
		storedWindowHashCap: cap,
		storedWindowHash: make(map[uint64]string, cap),
		hardFallbackBlocks: 1000,
		topics: topics,
	}
}

func (p *Processor) Run(ctx context.Context) error{
outer:
	for {		
		rpcCtx, rpcCancel := context.WithCancel(ctx)

		// compute for new head
		headHex, err := p.rpc.Head(rpcCtx)
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
		if p.opts.Confimation > 0 {
			conf = p.opts.Confimation
		}

		// Get the target block
		target := uint64(0)
		if head > conf {
			target = head - conf
		}

		n := p.opts.FetcherConcurrency
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
			rs := uint64(p.opts.RangeSize)

			for from := p.cursor + 1; from <= target; from += rs {
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

					switch p.opts.FetchMode {
					case FetchModeLogs:
						filter := Filter{
							FromBlock: Uint64ToHexQty(job.from),
							ToBlock: Uint64ToHexQty(job.to),
							Topics: p.topics,
						}
						logs, err = p.rpc.GetLogs(rpcCtx, filter)

					case FetchModeReceipts:
						logs, err = p.fetchLogsFromReceipts(rpcCtx, job.from, job.to)
					}
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
			next := p.cursor + 1

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
						block, err := p.rpc.GetBlock(rpcCtx, Uint64ToHexQty(next))
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
						parent, ok := p.storedWindowHash[next - 1]
						if (ok && block.ParentHash != parent) {
							log.Println("Hash mismatch, reorg happened...")
							rpcCancel()
							ancestor := p.handleReorg(ctx)

							p.cursor = ancestor
							return

						} else {
							log.Printf("Processed log from block %d to block %d...\n", next, end)
							// Commit logs to log channel
							if logs := windowLogs[next]; len(logs) > 0 {
								for _, l:= range logs {
								  select {
								  case <-rpcCtx.Done():
									return
								  case p.logsCh <- l:
								  }
								}
							}
							
							delete(windowLogs, next)
							delete(window, next)	
							p.cursor = end
							next = end + 1
						}
						
						// Get the end block blockhash after committing
						block, err = p.rpc.GetBlock(rpcCtx, Uint64ToHexQty(end))
						if err != nil {
							if rpcCtx.Err() != nil { return }        // batch was canceled; ignore
							log.Println("Error getting window end block: ", err)
							select { case errCh <- err: default: }
							return
						}

						p.storeWindowHash(end, block.Hash)
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

// return the read-only channel
func (p *Processor) Logs() <-chan Log {
	return p.logsCh
}

// During ancestor lookup we start from the cursor window and get to the window head and compare to the previous window
func (p *Processor) handleReorg(ctx context.Context) uint64 {
	ancestor := p.cursor
	for i := uint64(0); i < p.storedWindowHashCap; i++ {

		fallback := p.cursor; if fallback > p.hardFallbackBlocks { fallback -= p.hardFallbackBlocks } else { fallback = 0 }

		windowHeadBlock, err := p.rpc.GetBlock(ctx, Uint64ToHexQty(ancestor + 1))
		if err != nil {
			return fallback
		}
		
		if windowHeadBlock.ParentHash == p.storedWindowHash[ancestor] {
			p.dropWindowHash(ancestor)
			log.Println("Found ancestor: ", ancestor)
			return ancestor
		}
		
		if ancestor < uint64(p.opts.RangeSize) {
			ancestor = 0
			break
		}
		ancestor -= uint64(p.opts.RangeSize)

		select{
		case<- ctx.Done():
			return fallback
		default:
		}
	}
	fallback := p.cursor; if fallback > p.hardFallbackBlocks { fallback -= p.hardFallbackBlocks } else { fallback = 0 }
	log.Println("Hard fallback triggered...")
	if fallback <= 0 {
		fallback = 0
	}
	p.dropWindowHash(fallback)
	return fallback
}

func (p *Processor) storeWindowHash(to uint64, blockHash string) {
	_, exist := p.storedWindowHash[to]
	if exist {
		p.storedWindowHash[to] = blockHash
	}else {
		l := len(p.windowOrder)
		if uint64(l) >= p.storedWindowHashCap {
			old := p.windowOrder[0]
			delete(p.storedWindowHash, old)
			p.windowOrder = p.windowOrder[1:]
		}

		p.storedWindowHash[to] = blockHash
		p.windowOrder = append(p.windowOrder, to)
	}
}

func (p *Processor) dropWindowHash(after uint64) {
		// walk tail backward removing entries > after
		i := len(p.windowOrder) - 1
		for i >= 0 && p.windowOrder[i] > after {
			delete(p.storedWindowHash, p.windowOrder[i])
			i--
		}

		p.windowOrder = p.windowOrder[:i+1]
}

// Helper function to get logs from receipts
func(p *Processor) fetchLogsFromReceipts(ctx context.Context, from uint64, to uint64) ([]Log, error){
	var allLogs []Log
	for blockNum := from; blockNum <= to; blockNum ++ {
		s_blockNum := Uint64ToHexQty(blockNum)
		receipts, err := p.rpc.GetBlockReceipts(ctx, s_blockNum)
		if err != nil {
			return nil, fmt.Errorf("failed to get receipts for block %d: %w", blockNum, err)
		}

		for _, receipt := range receipts {
			for _, log := range receipt.Logs {
				if p.matchesTopicFilter(log) {
                    allLogs = append(allLogs, log)
                }
			}
		}
	}
	return allLogs, nil
}

// Checks if a log matches the configurated topic
func(p *Processor) matchesTopicFilter(log Log) bool {
	// If there is no topic specified then its true by default
	if len(p.opts.Topics) == 0 {
		return true
	}

	// Check if log has enough topics
    if len(log.Topics) == 0 {
        return false
    }

	// Match first topic (event signature)
    for _, filterTopic := range p.topics {
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





