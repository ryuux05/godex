package processor

import (
	"context"

	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
)

func (p *Processor) fetchAll(ctx context.Context, chain *chainState, jobs <-chan BlockRange) <- chan FetchResult {

}

func (p *Processor) fetch(ctx context.Context, chain *chainState, job BlockRange) FetchResult {
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
		return FetchResult{Range: job, Err: err}
	}


}

func (p *Processor) fetchTimestamps(ctx context.Context, chain *chainState, logs[]types.Log) 