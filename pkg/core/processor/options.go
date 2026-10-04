package processor

import (
	"fmt"
	"slices"
	"time"

	"github.com/ryuux05/godex/pkg/core/rpc"
)

type FetchMode string

const (
	FetchModeLogs     FetchMode = "logs"     // Use eth_getlogs for efficiency
	FetchModeReceipts FetchMode = "receipts" // Use eth_getBlockReceipts for reliability
)

type Options struct {
	// PollInterval is the delay between head checks when caught up. Default: 1s.
	PollInterval time.Duration
	// MaxInFlightRanges bounds fetched ranges awaiting ordered commits.
	// Default: twice FetcherConcurrency.
	MaxInFlightRanges int
	// SkipDecodeErrors explicitly permits advancing past logs that fail decoding.
	// Default false: stop without committing the affected window.
	SkipDecodeErrors bool
	// BatchSize controls how many decoded events are buffered and written to sinks at once.
	BatchSize int
	// RangeSize is the number of blocks requested per eth_getLogs window.
	// Larger ranges reduce round-trips but may exceed provider limits; tune per provider.
	RangeSize int
	// DecoderConcurrency spawns number of goroutine for decoder
	// Set to 1 for strictly serial processing.
	//DecoderConcurrency int

	// FetcherConcurrency spwawns number of goroutine for fetcher.
	// Set 1 for strictly serial fetching.
	FetcherConcurrency int
	// StartBlock is the initial cursor height; indexing begins at StartBlock + 1.
	// A higher stored cursor takes precedence. Use 0 to resume stored progress.
	StartBlock uint64
	// Confimation is range of block to wait.
	// Confirmation is used to avoid most reorgs.
	// Eth PoS confirmation is around 5-15 for "safe"
	ConfirmationDepth uint64
	// EnableTimestamps allow you to get timestamps for each event.
	// Note that enabling this would cost additional call to the RPC.
	// Default: false
	EnableTimestamps bool
	// How many Log items can be buffered in the processor’s logs channel.
	// 0 makes it unbuffered.
	// use a sane default (e.g., 1024).
	//LogsBufferSize uint64

	// ReorgLookbackBlocks is the maximum number of blocks to walk back when detecting a reorg. Used to bound header lookups and the size of stored window hashes.
	// Default: 64 (good starting point)
	ReorgLookbackBlocks uint64
	// Topics is the event for indexer to listen and get the log
	Topics [][]string
	// Addresses is a list of whitelisted addresses to filter
	Addresses []string
	// FetchMode determines which RPC method to use for fetching logs
	// - "logs": Uses eth_getLogs (default, more efficient)
	// - "receipts": Uses eth_getBlockReceipts (more reliable, higher bandwidth)
	FetchMode FetchMode
	// UseLogsForHistoricalSync determine whether to use eth_getlogs during historical sync
	// Using eth_getlogs instead of eth_getBlockReceipts during historical sync can save up rpc cost
	// Default: true
	UseLogsForHistoricalSync bool
	// RetryConfig manage how to handle retry on retriable errors.
	// Use pointer since it nillable
	// There is default settings
	RetryConfig *rpc.RetryConfig
}

func normalizeOptions(opts *Options) (*Options, error) {
	if opts == nil {
		return nil, fmt.Errorf("options are required")
	}
	copy := *opts
	if copy.RangeSize <= 0 {
		return nil, fmt.Errorf("RangeSize must be positive")
	}
	if copy.FetcherConcurrency <= 0 || copy.FetcherConcurrency > int(^uint(0)>>1)/2 {
		return nil, fmt.Errorf("FetcherConcurrency must be positive and representable")
	}
	if copy.BatchSize < 0 || copy.PollInterval < 0 || copy.MaxInFlightRanges < 0 {
		return nil, fmt.Errorf("BatchSize, PollInterval and MaxInFlightRanges cannot be negative")
	}
	if copy.FetchMode == "" {
		copy.FetchMode = FetchModeLogs
	}
	if copy.FetchMode != FetchModeLogs && copy.FetchMode != FetchModeReceipts {
		return nil, fmt.Errorf("unsupported FetchMode %q", copy.FetchMode)
	}
	if copy.PollInterval == 0 {
		copy.PollInterval = time.Second
	}
	if copy.MaxInFlightRanges == 0 {
		copy.MaxInFlightRanges = 2 * copy.FetcherConcurrency
	}
	if copy.ReorgLookbackBlocks == 0 {
		copy.ReorgLookbackBlocks = 64
	}
	cfg := rpc.DefaultRetryConfig()
	if opts.RetryConfig != nil {
		cfg = *opts.RetryConfig
		defaults := rpc.DefaultRetryConfig()
		if cfg.PerRequestTimeout == 0 {
			cfg.PerRequestTimeout = defaults.PerRequestTimeout
		}
		if cfg.Multiplier == 0 {
			cfg.Multiplier = defaults.Multiplier
		}
		if cfg.MaxBackoff == 0 {
			cfg.MaxBackoff = defaults.MaxBackoff
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("RetryConfig: %w", err)
	}
	copy.RetryConfig = &cfg
	copy.Addresses = slices.Clone(opts.Addresses)
	copy.Topics = make([][]string, len(opts.Topics))
	for i, topics := range opts.Topics {
		copy.Topics[i] = slices.Clone(topics)
	}
	return &copy, nil
}

type cursorState struct {
	// BlockNum is cursor block number stored inside persistant storage
	BlockNum uint64
	// BlockNum is cursor block number stored inside persistant storage
	BlockHash string
}

type ChainInfo struct {
	// Chain identification
	// Convert to string incase of integer chain id
	ChainId string
	// Name of the chain
	Name string
	// RPC information of the chain.
	RPC rpc.RPC
}
