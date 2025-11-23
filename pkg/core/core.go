package core

// Import all subpackages
import (
	"github.com/ryuux05/godex/pkg/core/metrics"
	"github.com/ryuux05/godex/pkg/core/processor"
	"github.com/ryuux05/godex/pkg/core/rpc"
	"github.com/ryuux05/godex/pkg/core/sink"
	"github.com/ryuux05/godex/pkg/core/types"
)

// ===== Re-export Types =====

// Processor types
type Processor = processor.Processor
type Options = processor.Options
type ChainInfo = processor.ChainInfo
type FetchMode = processor.FetchMode

const (
    FetchModeLogs     FetchMode = processor.FetchModeLogs
    FetchModeReceipts FetchMode = processor.FetchModeReceipts
)
// Decoder types

// Sink types
type Sink = sink.Sink

// RPC types
type RPC = rpc.RPC
type HTTPRPC = rpc.HTTPRPC

// Blockchain types
type Log = types.Log
type Block = types.Block
type Receipt = types.Receipt
type Filter = types.Filter
type Address = types.Address

// Metrics types
type Metrics = metrics.Metrics

// ===== Re-export Constructors =====

// Processor
var NewProcessor = processor.NewProcessor

// RPC
var NewHTTPRPC = rpc.NewHTTPRPC
