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
	Rollback(ctx context.Context, chainID string, toBlock uint64) error
}

type CursorStore interface {
	// Load is to load the current cursor state in case of restart, or downtime
    Load(ctx context.Context, chainID string) (blockNum uint64, blockHash string, err error)
	// Save is to save the newest cursor state.
    Save(ctx context.Context, chainID string, blockNum uint64, blockHash string) error
}