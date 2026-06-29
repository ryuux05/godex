package processor

import (
	"context"
	"fmt"
	"log/slog"

	coreerrors "github.com/ryuux05/godex/pkg/core/errors"
	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
)

// detectReorg is a function to detect reorg by comparing current block parent hash
// with stored previous window "to" hash
func (p *Processor) detectReorg(ctx context.Context, chain *chainState, currentBlockNum uint64, block types.Block) error {
	parent, ok := chain.blockHashCache.Get(currentBlockNum - 1) 
	if ok && block.ParentHash != parent {
		p.logger.Warn("hash mismatch, reorg detected", slog.String("chain_id", chain.chainInfo.ChainId),
						slog.Uint64("block", currentBlockNum))

		// Metrics to measure reorgs
		p.metrics.IncReorgs(chain.chainInfo.ChainId)

		ancestor, hash := p.handleReorg(ctx, chain)

		// Perform db rollback
		if err := p.sink.Rollback(ctx, chain.chainInfo.ChainId, ancestor, hash); err != nil {
			return fmt.Errorf("rollback failed after reorg at block %d: %w", ancestor, err)
		}

		// Update the chain cursor to ancestor
		chain.cursor.BlockHash = hash
		chain.cursor.BlockNum = ancestor

		return &coreerrors.ReorgError{
			BlockNum:  currentBlockNum,
			BlockHash: block.Hash,
		}
	}
	return nil
}

// During ancestor lookup we start from the cursor window and get to the window head and compare to the previous window
func (p *Processor) handleReorg(ctx context.Context, chain *chainState) (uint64, string) {
	// Conservative fallback: step back hardFallbackBlocks from cursor
	fallback := func() (uint64, string) {
		fb := chain.cursor.BlockNum
		if fb > chain.hardFallbackBlocks {
			fb -= chain.hardFallbackBlocks
		} else {
			fb = 0
		}

		chain.blockHashCache.DropAfter(fb)

		// Try cache first
		if h, ok := chain.blockHashCache.Get(fb); ok && h != "" {
			return fb, h
		}

		// Try RPC (best-effort, but don't hang forever)
		var h string
		_ = rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
			reqCtx, cancel := context.WithTimeout(ctx, chain.opts.RetryConfig.PerRequestTimeout)
			defer cancel()

			b, err := chain.chainInfo.RPC.GetBlock(reqCtx, utils.Uint64ToHexQty(fb))
			if err != nil {
				return err
			}
			h = b.Hash
			return nil
		})

		return fb, h
	}

	ancestor := chain.cursor.BlockNum

	// If we don't even have the cursor hash, we can't do a precise startup check.
	// Do a conservative fallback.
	if ancestor == 0 && chain.cursor.BlockHash == "" && chain.blockHashCache.Len() == 0 {
		return fallback()
	}

	// Bound the number of steps so we never loop forever
	// With your invariant (fixed RangeSize windows), this is enough.
	maxSteps := uint64(chain.blockHashCache.capacity)
	if maxSteps < 1 {
		maxSteps = 1
	}

	for steps := uint64(0); steps < maxSteps; steps++ {
		// Determine expected hash at this ancestor:
		// - Prefer cache
		// - If ancestor equals cursor, use cursor hash
		var expectedHash string
		if h, ok := chain.blockHashCache.Get(ancestor); ok && h != "" {
			expectedHash = h
		} else if ancestor == chain.cursor.BlockNum && chain.cursor.BlockHash != "" {
			expectedHash = chain.cursor.BlockHash
		} else {
			// cache miss at a non-cursor ancestor => can't validate precisely => fallback
			p.logger.Warn("cache miss during reorg, hard fallback",
				slog.String("chain_id", chain.chainInfo.ChainId),
				slog.Uint64("ancestor", ancestor),
			)
			return fallback()
		}

		// Fetch header for ancestor+1 and compare its parent hash
		nextNum := ancestor + 1
		nextHex := utils.Uint64ToHexQty(nextNum)

		var nextBlock types.Block
		err := rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
			reqCtx, cancel := context.WithTimeout(ctx, chain.opts.RetryConfig.PerRequestTimeout)
			defer cancel()

			b, err := chain.chainInfo.RPC.GetBlock(reqCtx, nextHex)
			if err != nil {
				return err
			}
			nextBlock = b
			return nil
		})
		if err != nil {
			// RPC unstable during reorg handling => conservative fallback
			return fallback()
		}

		if nextBlock.ParentHash == expectedHash {
			// Found canonical ancestor; drop anything after it
			chain.blockHashCache.DropAfter(ancestor)
			p.logger.Debug("found reorg ancestor",
				slog.String("chain_id", chain.chainInfo.ChainId),
				slog.Uint64("ancestor_block", ancestor),
			)
			return ancestor, expectedHash
		}

		// Step back by one window 
		if ancestor < uint64(chain.opts.RangeSize) {
			ancestor = 0
		} else {
			if chain.isLive {
				ancestor -= uint64(1)
			} else {
				ancestor -= uint64(chain.opts.RangeSize)
			}
		}

		select {
		case <-ctx.Done():
			return fallback()
		default:
		}
	}

	// If we exhausted our step budget, fallback
	p.logger.Warn("reorg ancestor not found within budget, hard fallback",
		slog.String("chain_id", chain.chainInfo.ChainId),
		slog.Uint64("cursor", chain.cursor.BlockNum),
	)
	return fallback()
}

// During processor continuation startup and reorg happened we need to do hardfallback.
// Since we dont have storedwindowhash to check.
func (p *Processor) handleStartupReorg(ctx context.Context, chain *chainState) uint64 {
	fallback := chain.cursor.BlockNum
	if fallback > chain.hardFallbackBlocks {
		fallback -= chain.hardFallbackBlocks
	} else {
		fallback = 0
	}

	p.logger.Warn("startup hard fallback triggered", slog.String("chain_id", chain.chainInfo.ChainId), slog.Uint64("fallback_block", fallback))
	if fallback <= 0 {
		fallback = 0
	}

	chain.blockHashCache.DropAfter(fallback)
	return fallback
}

