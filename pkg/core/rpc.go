package core

import "context"


type RPC interface {
	// Get the current best block height
	Head(ctx context.Context) (string, error)

	// Get block for current block number
	GetBlock(ctx context.Context, blockNumber string) (Block, error)

	// Fetch logs over a range with filter
	GetLogs(ctx context.Context, filter Filter) ([]Log, error)
}
