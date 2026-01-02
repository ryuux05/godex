package processor

import (
	"context"
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
			p.logger.Error("failed to rollback sink", slog.String("chain_id", chain.chainInfo.ChainId), slog.Any("error", err))
		}

		// Update the chain cursor to ancestor
		chain.cursor.BlockHash = hash
		chain.cursor.BlockNum = ancestor

		return &coreerrors.ReorgError{
			BlockNum: currentBlockNum,
			BlockHash: block.Hash,
		}
	}
	return nil
}

// During ancestor lookup we start from the cursor window and get to the window head and compare to the previous window
func (p *Processor) handleReorg(ctx context.Context, chain *chainState) (uint64, string)  {
	ancestor := chain.cursor.BlockNum

	// Helper to get fallback with hash
    getFallback := func() (uint64, string) {
        fallback := chain.cursor.BlockNum
        if fallback > chain.hardFallbackBlocks {
            fallback -= chain.hardFallbackBlocks
        } else {
            fallback = 0
        }
        chain.blockHashCache.DropAfter(fallback)

        // Try to get hash from cache or RPC
        hash, ok := chain.blockHashCache.Get(fallback)
        if !ok {
            block, err := chain.chainInfo.RPC.GetBlock(ctx, utils.Uint64ToHexQty(fallback))
            if err == nil {
                hash = block.Hash
            }
        }
        return fallback, hash
    }

	for i := uint64(0); i < uint64(chain.blockHashCache.capacity); i++ {

		fallback := chain.cursor.BlockNum
		if fallback > chain.hardFallbackBlocks {
			fallback -= chain.hardFallbackBlocks
		} else {
			fallback = 0
		}
		
		var windowHeadBlock types.Block
		err := rpc.RetryWithBackoff(ctx, *chain.opts.RetryConfig, func() error {
			var err error
			windowHeadBlock, err = chain.chainInfo.RPC.GetBlock(ctx, utils.Uint64ToHexQty(ancestor + 1))
			
			if err != nil {
				return err
			}
			return nil
		})

		// When we cant connect to rpc we return fallback with empty hash
		if err != nil {
			fallback, hash := getFallback()
			return fallback, hash
		}
	

		ancestorHash, e := chain.blockHashCache.Get(ancestor)
		if !e {
			// hardfallback if ancestor didnt exists
			fallback := chain.cursor.BlockNum
			if fallback > chain.hardFallbackBlocks {
				fallback -= chain.hardFallbackBlocks
			} else {
				fallback = 0
		}
			p.logger.Warn("cache miss during reorg, hard fallback triggered", slog.String("chain_id", chain.chainInfo.ChainId), slog.Uint64("fallback_block", fallback))
			chain.blockHashCache.DropAfter(fallback)
			
			fallback, hash := getFallback()
			return fallback, hash
		}
		if windowHeadBlock.ParentHash == ancestorHash {
			chain.blockHashCache.DropAfter(ancestor)
			p.logger.Debug("found reorg ancestor", slog.String("chain_id", chain.chainInfo.ChainId), slog.Uint64("ancestor_block", ancestor))
			return ancestor, ancestorHash
		}

		if ancestor < uint64(chain.opts.RangeSize) {
			ancestor = 0
			break
		}
		ancestor -= uint64(chain.opts.RangeSize)

		select {
		case <-ctx.Done():
			fallback, hash := getFallback()
			return fallback, hash
		default:
		}
	}
	fallback := chain.cursor.BlockNum
	if fallback > chain.hardFallbackBlocks {
		fallback -= chain.hardFallbackBlocks
	} else {
		fallback = 0
	}
	p.logger.Warn("hard fallback triggered", slog.String("chain_id", chain.chainInfo.ChainId), slog.Uint64("fallback_block", fallback))
	if fallback <= 0 {
		fallback = 0
	}
	chain.blockHashCache.DropAfter(fallback)
	fallback, hash := getFallback()
	return fallback, hash
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

