package core

type Options struct {
	// BatchSize controls how many decoded events are buffered and written to sinks at once.
	BatchSize int
	// RangeSize is the number of blocks requested per eth_getLogs window.
	// Larger ranges reduce round-trips but may exceed provider limits; tune per provider.
	RangeSize int
	// MaxParralel limits the number of concurrent range workers (RPC calls and decodes).
	// Set to 1 for strictly serial processing.
	MaxParralel int 
	// StartBlock is the inclusive block height to begin indexing from.
	// Use 0 to let the processor derive it (e.g., from a stored cursor).
	StartBlock uint64
	// EndBlock is an optional inclusive block height to stop indexing at.
	// Use 0 to run continuously toward the moving head.
	EndBlock uint64
	// Confimation is range of block to wait.
	// Confirmation is used to avoid most reorgs.
	// Eth PoS confirmation is around 5-15 for "safe"
	Confimation uint64
}