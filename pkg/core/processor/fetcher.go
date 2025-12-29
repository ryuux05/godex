package processor

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
	"golang.org/x/sync/errgroup"
)

func (p *Processor) fetchAll(ctx context.Context, chain *chainState, jobs <-chan BlockRange) (<- chan FetchResult, error) {
	results := make(chan FetchResult, chain.opts.FetcherConcurrency)
	g := new(errgroup.Group)
	for i := 0; i < chain.opts.FetcherConcurrency; i++ {
		g.Go(func() error {
			return p.fetchWorker(ctx, chain, jobs, results)
		})
	}
	go func() {
        if err := g.Wait(); err != nil {
            p.logger.Error("fetch worker failed", slog.Any("error", err))
        }
        close(results)
    }()
    
    return results, nil
}

func (p *Processor) fetchWorker(ctx context.Context, chain *chainState, jobs <-chan BlockRange, results chan<- FetchResult) error {
	for job := range jobs {
        result, err := p.fetch(ctx, chain, job)
		if err != nil {
            return err 
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case results <- result:
        }
    }
	return nil
}

func (p *Processor) fetch(ctx context.Context, chain *chainState, job BlockRange) (FetchResult, error) {
	var logs []types.Log
	var err error
	// When the chain is live
	err = rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
		mode := chain.opts.FetchMode

		if !chain.isLive && chain.opts.UseLogsForHistoricalSync {
			mode = FetchModeLogs
		}

		switch mode {
		case FetchModeLogs:
			filter := types.Filter{
				FromBlock: utils.Uint64ToHexQty(job.From),
				ToBlock:   utils.Uint64ToHexQty(job.To),
				Topics:    chain.topics,
				Address:   chain.addresses,
			}

			// Record fetch time
			logs, err = chain.chainInfo.RPC.GetLogs(ctx, filter)

		case FetchModeReceipts:
			logs, err = p.fetchLogsFromReceipts(ctx, job.From, job.To, chain)

		}

		return err
	})

	if err != nil {
		return FetchResult{Range: job}, err
	}

	// Fetch timestamps if enabled
    var timestamps map[uint64]uint64
    if chain.opts.EnableTimestamps && len(logs) > 0 {
        timestamps, err = p.fetchTimestamps(ctx, chain, logs)
		if err != nil {
			return FetchResult{Range: job}, err
		}
    }
    
    return FetchResult{
        Range:      job,
        Logs:       logs,
        Timestamps: timestamps,
    }, nil
}

func (p *Processor) fetchTimestamps(ctx context.Context, chain *chainState, logs[]types.Log) (map[uint64]uint64, error) {
	// Collect unique block numbers
	uniqueBlocks := make(map[uint64]struct{})
	for _, l := range logs {
		bn, err := utils.HexQtyToUint64(l.BlockNumber)
		if err != nil {
			p.logger.Warn("failed to parse block number", slog.String("block_number", l.BlockNumber), slog.Any("error", err))
			continue
		}
		uniqueBlocks[bn] = struct{}{}
	}

	// Convert to slice for batch request
	blockNumbers := make([]string, 0, len(uniqueBlocks))
	for bn := range uniqueBlocks {
		blockNumbers = append(blockNumbers, utils.Uint64ToHexQty(bn))
	}

	// Try batch fetch first, fall back to individual calls
	timestamps := make(map[uint64]uint64)
	// Try batch first with retry
    var blocks map[string]types.Block
    err := rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
        var err error
        blocks, err = chain.chainInfo.RPC.GetBlocks(ctx, blockNumbers)
        return err
    })

	 
    if err != nil {
        return nil, fmt.Errorf("failed to get timestamp blocks: %w", err)
    }
    
	// Process batch results
	for hexBn, blk := range blocks {
		bn, err := utils.HexQtyToUint64(hexBn)
		if err != nil {
            return nil, fmt.Errorf("parse block number %s: %w", hexBn, err)
        }
		ts, err := utils.HexQtyToUint64(blk.Timestamp)
		if err != nil {
            return nil, fmt.Errorf("parse timestamp for block %d: %w", bn, err)
        }
		timestamps[bn] = ts
	}
	
	return timestamps, nil
}