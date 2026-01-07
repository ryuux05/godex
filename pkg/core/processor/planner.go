package processor

import (
	"context"

	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/utils"
)

// Function to plan job
// A single routine to send jobs (from, to) block to jobs channel
// Job channel will ack as backpressure when fetcher slows down.
func (p *Processor) planJobs(ctx context.Context, chain *chainState) (<-chan BlockRange, uint64, error) {
    head, err := p.getHead(ctx, chain)
    if err != nil {
        return nil, 0, err
    }
    
    target := head - chain.opts.ConfirmationDepth
    chain.progress.SetHead(head)
    
    // Already caught up
    if chain.cursor.BlockNum >= target {
		chain.isLive = true
        ch := make(chan BlockRange)
        close(ch)
        return ch, target, nil
    }
    
    jobs := make(chan BlockRange, chain.opts.FetcherConcurrency)
    
    go func() {
        defer close(jobs)
        
		// When chain is live make range size to one for optimal reorg handling`
		var rs uint64
		if !chain.isLive {
			rs = uint64(chain.opts.RangeSize)
		} else {
			rs = uint64(1)
		}
		
        for from := chain.cursor.BlockNum + 1; from <= target; from += rs {
            to := from + rs - 1
            if to > target {
                to = target
            }
            
            select {
            case <-ctx.Done():
                return
            case jobs <- BlockRange{From: from, To: to}:
            }
        }
    }()
    
    return jobs, target, nil
}

func (p *Processor) getHead(ctx context.Context, chain *chainState) (uint64, error) {
    var headHex string
    err := rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
        var err error
        headHex, err = chain.chainInfo.RPC.Head(ctx)
        return err
    })
    if err != nil {
        return 0, err
    }
    return utils.HexQtyToUint64(headHex)
}
