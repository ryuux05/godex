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

	var target uint64
	if head > chain.opts.ConfirmationDepth {
		target = head - chain.opts.ConfirmationDepth
	}
	chain.progress.SetHead(head)

	// Already caught up
	if chain.cursor.BlockNum >= target {
		chain.pendingRanges = nil
		chain.isLive.Store(true)
		ch := make(chan BlockRange)
		close(ch)
		return ch, target, nil
	}

	jobs := make(chan BlockRange, chain.opts.FetcherConcurrency)
	pendingRanges := make(chan struct{}, chain.opts.MaxInFlightRanges)
	chain.pendingRanges = pendingRanges

	start := chain.cursor.BlockNum + 1
	go func() {
		defer close(jobs)

		// When chain is live make range size to one for optimal reorg handling`
		var rs uint64
		if !chain.isLive.Load() {
			rs = uint64(chain.opts.RangeSize)
		} else {
			rs = uint64(1)
		}

		for from := start; from <= target; {
			span := rs - 1
			if span > target-from {
				span = target - from
			}
			to := from + span
			select {
			case <-ctx.Done():
				return
			case pendingRanges <- struct{}{}:
			}

			select {
			case <-ctx.Done():
				return
			case jobs <- BlockRange{From: from, To: to}:
			}
			if to == target {
				return
			}
			from = to + 1
		}
	}()

	return jobs, target, nil
}

func (p *Processor) getHead(ctx context.Context, chain *chainState) (uint64, error) {
	var headHex string
	err := rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
		reqCtx, cancel := context.WithTimeout(ctx, chain.opts.RetryConfig.PerRequestTimeout)
		defer cancel()
		var err error
		headHex, err = chain.chainInfo.RPC.Head(reqCtx)
		return err
	})
	if err != nil {
		return 0, err
	}
	return utils.HexQtyToUint64(headHex)
}
