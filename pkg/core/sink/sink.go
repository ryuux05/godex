package sink

import (
	"context"

	"github.com/ryuux05/godex/pkg/core/types"
)


type Sink interface {
	// StoreBatch persists events from multiple blocks efficiently
	// This is useful for batch operations and better performance
	Store(ctx context.Context, events []types.Event) error
	// Rollback removes all events from a block number onwards
	// Used during reorg handling to remove orphaned blocks
	Rollback(ctx context.Context, chainId string, toBlock uint64, blockHash string) error
	// LoadCursor load the cursor from persistence storage to continue indexing
	// returns block nunber along with block hash to do reorg check
	LoadCursor(ctx context.Context, chainId string) (blockNum uint64, blockHash string, err error)
	// UpdateCursor store the current block number and block hash to persistence storage
	UpdateCursor(ctx context.Context, chainId string, newBlock uint64, blockHash string) error
}

