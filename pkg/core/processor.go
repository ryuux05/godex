package core

import (
	"context"
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

	return &Processor{
		rpc: rpc,
		opts: opts,
		cursor: cursor,
		logsCh: make(chan Log, opts.LogsBufferSize),
		storedWindowHashCap: cap,
		storedWindowHash: make(map[uint64]string, cap),
	}
}

func (p *Processor) Run(ctx context.Context) error{
	
	for {		
		rpcCtx, rpcCancel := context.WithCancel(ctx)
		defer rpcCancel()
		
		// compute for new head
		headHex, err := p.rpc.Head(rpcCtx)
		if err != nil {
			return err
		}

		head, err := HexQtyToUint64(headHex)
		if err != nil {
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
				case <-ctx.Done():
					return
				case jobs <- blockRange{from, to}:
				}
			} 
		}()
				
				
				
		// create waitgroup and make error channel
		var wg sync.WaitGroup
		wg.Add(n)
		errCh := make(chan error, 1)
		
		doneCh := make(chan blockRange, n)

		for i := 0; i < n; i++ {
			go func(){
				defer wg.Done()
				for job := range jobs {
					filter := Filter{
						FromBlock: Uint64ToHexQty(job.from),
						ToBlock: Uint64ToHexQty(job.to),
						Topics: []any {
							"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"},
					}
					logs, err := p.rpc.GetLogs(rpcCtx, filter)
					if err != nil {
						select {
						case errCh <- err:
						default:
							return
						}
					}
					for _, log := range logs {
						select {
						case <-ctx.Done():
							return
						case p.logsCh <- log:
						}
					}				
					doneCh <- blockRange{job.from, job.to}
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
		go func() {
			window := make(map[uint64]uint64)
			blockHash := make(map[uint64]string)
			next := p.cursor + 1

			for {
				select {
				case <-ctx.Done():
					return
				case done, ok := <-doneCh:
					if !ok {return};
					window[done.from] = done.to

					for end, ok2 := window[next]; ok2; end, ok2 = window[next] {
						// Get start window blockhash and compare it with the stored blockhash
						block, err := p.rpc.GetBlock(rpcCtx, Uint64ToHexQty(next))
						if err != nil {
							return
						}

						//Compare to parents
						if (block.ParentHash != blockHash[next - 1]) {
							log.Println("Hash mismatch, reorg happened...")
							rpcCancel()
							p.handleReorg()
							return
						}

						delete(window, next)	
						p.cursor = end
						next = end + 1

						// Get the end block blockhash after committing
						block, err = p.rpc.GetBlock(ctx, Uint64ToHexQty(end))
						if err != nil {
							return
						}
						p.storeWindowHash(done.to, block.Hash)
					}
				}
			}
		}()
		
		// listen to condition channel
		for{

			select {
			case <-ctx.Done():
				return nil
			case <-done:
			case err := <-errCh:
				<-done
				return err
			}
		}
	}
}

func (p *Processor) AddLog() {

}

// return the read-only channel
func (p *Processor) Logs() <-chan Log {
	return p.logsCh
}

func (p *Processor) handleReorg() {

}

func (p *Processor) storeWindowHash(to uint64, blockHash string) {
	l := len(p.windowOrder)
	if uint64(l) >= p.storedWindowHashCap {
		old := p.windowOrder[0]
		delete(p.storedWindowHash, old)
		p.windowOrder = p.windowOrder[1:]
	}
	p.storedWindowHash[to] = blockHash
}




