// Package godex is the public API for the Godex blockchain indexing SDK.
//
// It intentionally exposes a small, stable surface:
// - Processor engine (multi-chain orchestration)
// - RPC client + retry configuration
// - Sink + Metrics interfaces
// - Core types and common errors
//
// Advanced/unstable internals (decoder router details, match helpers, etc.) remain
// available via subpackages (e.g. pkg/core/decoder) without being part of the
// top-level compatibility contract.
//
// Quick Start:
//
//	import (
//	    "context"
//	    "github.com/ryuux05/godex/pkg/godex"
//	    "github.com/ryuux05/godex/pkg/core/decoder"
//	)
//
//	func main() {
//	    ctx := context.Background()
//
//	    rpc := godex.NewHTTPRPC("https://...", 20, 5)
//
//	    sink := /* your sink */
//	    m := godex.NoopMetrics{}
//
//	    p := godex.NewProcessor(m, sink)
//
//	    opts := &godex.Options{
//	        RangeSize:          1000,
//	        FetcherConcurrency: 4,
//	        StartBlock:         18_000_000,
//	        ConfirmationDepth:  12,
//	        Topics: [][]string{{"0xddf252ad..."}}, // topic0 OR-list
//	    }
//
//	    router := decoder.NewDecoderRouter()
//	    // router.Register(...)
//
//	    p.AddChain(godex.ChainInfo{ChainId: "1", Name: "Ethereum", RPC: rpc}, opts, router)
//	    _ = p.Run(ctx)
//	}
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

func NewProcessor(m Metrics, s Sink) *Processor {
	return processor.NewProcessor(m, s)
}

// ============================================================================
// RPC API (stable)
// ============================================================================

type RPC = rpc.RPC
type HTTPRPC = rpc.HTTPRPC
type RetryConfig = rpc.RetryConfig

func NewHTTPRPC(endpoint string, rateLimit uint16, burstLimit uint16) *HTTPRPC {
	return rpc.NewHTTPRPC(endpoint, rateLimit, burstLimit)
}

func DefaultRetryConfig() RetryConfig {
	return rpc.DefaultRetryConfig()
}

// ============================================================================
// Sink + Metrics (stable)
// ============================================================================

type Sink = sink.Sink
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

func IsRetryableError(err error) bool {
	return coreerrors.IsRetryableError(err)
}

func IsResponseTooBigError(err error) bool {
	return coreerrors.IsResponseTooBigError(err)
}
