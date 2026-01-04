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

func (p *Processor) fetchAll(ctx context.Context, chain *chainState, jobs <-chan BlockRange) (<-chan FetchResult, <-chan struct{}, error) {
	results := make(chan FetchResult, chain.opts.FetcherConcurrency)
	done := make(chan struct{})
	g := new(errgroup.Group)
	for i := 0; i < chain.opts.FetcherConcurrency; i++ {
		g.Go(func() error {
			// Each fetcher gets its own context with timeout
			fetcherCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			return p.fetchWorker(fetcherCtx, chain, jobs, results)
		})
	}
	go func() {
		// wait for all fetcher to exit
		g.Wait()
		close(results)
		// wait for the done signal
		// signaling that the fetcher is done
		close(done)
	}()

	return results, done, nil
}

func (p *Processor) fetchWorker(ctx context.Context, chain *chainState, jobs <-chan BlockRange, results chan<- FetchResult) error {
	for job := range jobs {
		// Check for context cancellation before starting fetch
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

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
	// Check context before starting
	select {
	case <-ctx.Done():
		return FetchResult{Range: job}, ctx.Err()
	default:
	}

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
			if err != nil {
				return err
			}

			// Add debug log
			p.logger.Debug("fetched logs", slog.Int("count", len(logs)), slog.Any("error", err))

		case FetchModeReceipts:
			logs, err = p.fetchLogsFromReceipts(ctx, job.From, job.To, chain)

		}

		return err
	})

	if err != nil {
		return FetchResult{Range: job}, err
	}

	// Check context before fetching timestamps
	select {
	case <-ctx.Done():
		return FetchResult{Range: job, Logs: logs}, ctx.Err()
	default:
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

func (p *Processor) fetchTimestamps(ctx context.Context, chain *chainState, logs []types.Log) (map[uint64]uint64, error) {
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

// Helper function to get logs from receipts
func (p *Processor) fetchLogsFromReceipts(ctx context.Context, from uint64, to uint64, chain *chainState) ([]types.Log, error) {
	var allLogs []types.Log
	for blockNum := from; blockNum <= to; blockNum++ {

		// Check context between blocks
		select {
		case <-ctx.Done():
			return allLogs, ctx.Err()
		default:
		}

		s_blockNum := utils.Uint64ToHexQty(blockNum)
		receipts, err := chain.chainInfo.RPC.GetBlockReceipts(ctx, s_blockNum)
		if err != nil {
			return nil, fmt.Errorf("failed to get receipts for block %d: %w", blockNum, err)
		}

		for _, receipt := range receipts {
			for _, log := range receipt.Logs {

				// Allow all addresses if addressSet is empty (no Addresses specified)
				if len(chain.addressSet) > 0 {
					if _, ok := chain.addressSet[utils.Normalize(log.Address)]; !ok {
						continue
					}
				}

				if !p.matchesTopicFilter(log, chain) {
					continue
				}

				allLogs = append(allLogs, log)
			}
		}
	}
	return allLogs, nil
}

// Checks if a log matches the configurated topic
func (p *Processor) matchesTopicFilter(log types.Log, chain *chainState) bool {
	// If there is no topic specified then its true by default
	if len(chain.opts.Topics) == 0 {
		return true
	}

	// Check if log has enough topics
	if len(log.Topics) == 0 {
		return false
	}

	// Match first topic (event signature)
	for i, filterTopics := range chain.topics {
		// Empty filter at this position means "match any"
		if len(filterTopics) == 0 {
			continue
		}

		// Log doesn't have enough topics for this filter position
		if i >= len(log.Topics) {
			return false
		}

		for _, topic0 := range filterTopics {
			if len(log.Topics) > 0 {
				logTopic := log.Topics[0]
				if logTopic == topic0 {
					return true
				}
			}
		}
	}

	return false
}
