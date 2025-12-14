package rpc

import (
	"context"

	"github.com/ryuux05/godex/pkg/core/types"
)


type RPC interface {
	// Get the current best block height
	Head(ctx context.Context) (string, error)

	// Get block for current block number
	GetBlock(ctx context.Context, blockNumber string) (types.Block, error)

	// Batch get multiple blocks
	GetBlocks(ctx context.Context, blockNumbers []string) (map[string]types.Block, error)
	
	// Fetch logs over a range with filter
	GetLogs(ctx context.Context, filter types.Filter) ([]types.Log, error)

	// Get block receipt for the current block number
	GetBlockReceipts(ctx context.Context, blockNumber string) ([]types.Receipt, error)
}
