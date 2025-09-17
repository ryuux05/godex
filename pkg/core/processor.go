package core

import (
	"context"
	"sync"
)


type Processor struct {
	// RPC instances where the indexer going to quer
	// Specify endpoint and rate-limit
	rpc RPC 
	// Cursor is a pointer that points the current block where the indexer is pointing 
	cursor uint64
	// logsChan is a channel where processor will store the indexed logs
	logsCh chan Log
	opts *Options
}

func NewProcessor(rpc RPC, opts *Options) *Processor {
	cursor := opts.StartBlock; if cursor == 0 { cursor = 0 }

	return &Processor{
		rpc: rpc,
		opts: opts,
		cursor: cursor,
		logsCh: make(chan Log, opts.LogsBufferSize),
	}
}

func (p *Processor) Run(ctx context.Context) error{
	for {		
		// compute for new head
		headHex, err := p.rpc.Head(ctx)
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
					}
					logs, err := p.rpc.GetLogs(ctx, filter)
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
			next := p.cursor + 1

			for {
				select {
				case <-ctx.Done():
					return
				case done, ok := <-doneCh:
					if !ok {return};
					window[done.from] = done.to

					for end, ok2 := window[next]; ok2; end, ok2 = window[next] {
						delete(window, next)	
						p.cursor = end
						next = end + 1
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


