package core

type FetchMode string

const (
	FetchModeLogs     FetchMode = "logs"     // Use eth_getlogs for efficiency
	FetchModeReceipts FetchMode = "receipts" // Use eth_getBlockReceipts for reliability
)

type Options struct {
	// BatchSize controls how many decoded events are buffered and written to sinks at once.
	BatchSize int
	// RangeSize is the number of blocks requested per eth_getLogs window.
	// Larger ranges reduce round-trips but may exceed provider limits; tune per provider.
	RangeSize int
	// DecoderConcurrency spawns number of goroutine for decoder
	// Set to 1 for strictly serial processing.
	DecoderConcurrency int
	// FetcherConcurrency spwawns number of goroutine for fetcher.
	// Set 1 for strictly serial fetching.
	FetcherConcurrency int
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
	//How many Log items can be buffered in the processor’s logs channel.
	// 0 makes it unbuffered.
	// use a sane default (e.g., 1024).
	LogsBufferSize uint64
	// ReorgLookbackBlocks is the maximum number of blocks to walk back when detecting a reorg. Used to bound header lookups and the size of stored window hashes.
	// Default: 64 (good starting point)
	ReorgLookbackBlocks uint64
	// Topics is the event for indexer to listen and get the log
	Topics []string
	// FetchMode determines which RPC method to use for fetching logs
	// - "logs": Uses eth_getLogs (default, more efficient)
	// - "receipts": Uses eth_getBlockReceipts (more reliable, higher bandwidth)
	FetchMode FetchMode
	// RetryConfig manage how to handle retry on retriable errors.
	// Use pointer since it nillable
	// There is default settings
	RetryConfig *RetryConfig
}

type ChainInfo struct {
	// Chain identification
	// Convert to string incase of integer chain id
	ChainId string
	// Name of the chain
	Name    string
	// RPC information of the chain.
	RPC     RPC
}

