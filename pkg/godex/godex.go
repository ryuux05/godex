// Package godex provides the public API for EVM event indexing.
//
// New assembles a single-chain indexer from Config, deriving event filters and
// decoder routes from contract ABIs. It supports caller-owned sinks, PostgreSQL
// pools, HTTP clients, loggers and metrics. FromBlock is the first block to index,
// inclusively; zero resumes progress or starts at block 1 without a cursor.
//
// NewProcessor and AddChainContext expose the lower-level API for custom routing
// and multi-chain orchestration. Run one active indexer per chain and namespace.
// See docs/sdk.md and examples/service for complete integrations.
package godex

import (
	coreerrors "github.com/ryuux05/godex/pkg/core/errors"
	"github.com/ryuux05/godex/pkg/core/metrics"
	"github.com/ryuux05/godex/pkg/core/processor"
	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/sink"
	"github.com/ryuux05/godex/pkg/core/types"
)

// ============================================================================
// Processor API (stable)
// ============================================================================

type Processor = processor.Processor
type Options = processor.Options
type ChainInfo = processor.ChainInfo
type FetchMode = processor.FetchMode

const (
	FetchModeLogs     FetchMode = processor.FetchModeLogs
	FetchModeReceipts FetchMode = processor.FetchModeReceipts
)

// NewProcessor creates a new blockchain indexing processor with the provided
// metrics collector and event sink. The processor orchestrates concurrent
// fetching, decoding, and persistence of blockchain events across multiple chains.
func NewProcessor(m Metrics, s Sink) *Processor {
	return processor.NewProcessor(m, s)
}

// ============================================================================
// RPC API (stable)
// ============================================================================

type RPC = rpc.RPC
type HTTPRPC = rpc.HTTPRPC
type HTTPRPCOptions = rpc.HTTPRPCOptions
type ChainIDRPC = rpc.ChainIDRPC
type RetryConfig = rpc.RetryConfig

// NewHTTPRPC creates a new rate-limited HTTP RPC client for blockchain interactions.
// The client handles rate limiting and context cancellation. The processor
// applies RetryConfig; direct RPC calls do not retry automatically.
func NewHTTPRPC(endpoint string, rateLimit uint16, burstLimit uint16) *HTTPRPC {
	return rpc.NewHTTPRPC(endpoint, rateLimit, burstLimit)
}

// NewHTTPRPCWithOptions validates an endpoint and borrows a service HTTP client.
func NewHTTPRPCWithOptions(endpoint string, opts HTTPRPCOptions) (*HTTPRPC, error) {
	return rpc.NewHTTPRPCWithOptions(endpoint, opts)
}

// DefaultOptions returns independent tuning defaults. Choose confirmation depth
// explicitly for your chain; it defaults to zero.
func DefaultOptions() Options {
	return processor.DefaultOptions()
}

// DefaultRetryConfig returns the default retry configuration with sensible
// defaults for RPC request retries, including exponential backoff and jitter.
func DefaultRetryConfig() RetryConfig {
	return rpc.DefaultRetryConfig()
}

// ============================================================================
// Sink + Metrics (stable)
// ============================================================================

type Sink = sink.Sink
type WindowSink = sink.WindowSink
type Metrics = metrics.Metrics
type NoopMetrics = metrics.Noop

// ============================================================================
// Core Types (stable)
// ============================================================================

type Event = types.Event
type EventFields = types.EventFields
type Log = types.Log
type Block = types.Block
type Receipt = types.Receipt
type Filter = types.Filter
type Address = types.Address

const ZeroAddress = types.ZeroAddress

// ============================================================================
// Errors (stable)
// ============================================================================

type HTTPError = coreerrors.HTTPError
type RPCError = coreerrors.RPCError
type ReorgError = coreerrors.ReorgError

var ErrCursorNotFound = coreerrors.ErrCursorNotFound
var ErrReorgDetected = coreerrors.ErrReorgDetected
var ErrFieldNotFound = types.ErrFieldNotFound

// IsRetryableError determines if an error should trigger a retry attempt.
// Returns true for transient errors like network timeouts, rate limits, or
// temporary RPC unavailability that may succeed on retry.
func IsRetryableError(err error) bool {
	return coreerrors.IsRetryableError(err)
}

// IsResponseTooBigError checks if an error indicates an RPC response exceeded
// the provider's size limits. This typically triggers automatic range splitting
// in the fetcher to reduce individual request sizes.
func IsResponseTooBigError(err error) bool {
	return coreerrors.IsResponseTooBigError(err)
}
