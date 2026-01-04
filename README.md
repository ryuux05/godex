# godex

A high-performance, production-ready blockchain indexing SDK written in Go for building scalable EVM-compatible blockchain indexers. Features automatic reorganization handling, intelligent multi-contract event routing, concurrent multi-chain processing, and structured event persistence with comprehensive observability.

## Features

- **Concurrent Multi-Chain Indexing**: Process events across multiple EVM-compatible chains simultaneously with individual chain isolation
- **Automatic Reorganization Handling**: Built-in detection and rollback for blockchain reorganizations with efficient ancestor recovery
- **Intelligent Event Routing**: DecoderRouter enables complex multi-contract scenarios with configurable match conditions
- **High-Performance Processing**: Concurrent fetching with configurable worker pools, batch RPC requests, and natural backpressure
- **Production-Ready Storage**: Transactional event persistence with atomic rollback support and cursor management
- **Flexible Event Decoding**: Pluggable ABI-based decoders with support for multiple contract standards
- **Comprehensive Observability**: Structured logging, metrics collection, and health monitoring
- **Resilient Error Handling**: Automatic retry with exponential backoff, context-aware cancellation, and graceful shutdown

## Installation

```bash
go get github.com/ryuux05/godex
```

## Quick Start

### Basic ERC20 Indexer Setup

```go
package main

import (
    "context"
    "log/slog"
    "os"

    "github.com/ryuux05/godex/pkg/core"
    "github.com/ryuux05/godex/pkg/core/decoder"
    "github.com/ryuux05/godex/adapters/sink/postgres"
)

func main() {
    // Initialize RPC client with rate limiting
    rpc := core.NewHTTPRPC("https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY", 20, 5)

    // Initialize PostgreSQL sink for event storage
    sink, err := postgres.NewPostgresSink(context.Background(),
        "postgres://user:pass@localhost:5432/godex?sslmode=disable")
    if err != nil {
        log.Fatal(err)
    }

    // Configure indexing parameters
    opts := &core.Options{
        RangeSize:              1000,  // Process 1000 blocks per batch
        FetcherConcurrency:     4,     // 4 concurrent RPC workers
        StartBlock:             18000000,
        ConfirmationDepth:      12,    // Wait 12 blocks for reorg safety
        EnableTimestamps:       true,  // Include block timestamps
        Topics: [][]string{{
            "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", // Transfer
        }},
        FetchMode:                core.FetchModeLogs,
        UseLogsForHistoricalSync: true,
    }

    // Define Ethereum mainnet configuration
    ethereumChain := core.ChainInfo{
        ChainId: "1",
        Name:    "Ethereum",
        RPC:     rpc,
    }

    // Initialize standard decoder and register ERC20 ABI
    dec := decoder.NewStandardDecoder()
    if err := dec.RegisterABI("ERC20", erc20ABI); err != nil {
        log.Fatal(err)
    }

    // Create processor with metrics and sink
    processor := core.NewProcessor(nil, sink)

    // Configure structured JSON logging
    logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
    }))
    processor.SetLogger(logger)

    // Register chain with decoder
    if err := processor.AddChain(ethereumChain, opts, dec); err != nil {
        log.Fatal(err)
    }

    // Start continuous indexing with graceful shutdown
    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    if err := processor.Run(ctx); err != nil && err != context.Canceled {
        log.Fatal(err)
    }
}
```

### Multi-Contract Indexer with Router

```go
// Create decoder router for multiple contract types
router := decoder.NewDecoderRouter()

// ERC20 contracts: 3 topics, Transfer event
router.Register(
    decoder.And(
        decoder.ByTopicCount(3),
        decoder.ByTopic0("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"),
    ),
    "ERC20",
    erc20Decoder,
)

// ERC721 contracts: 4 topics, Transfer event
router.Register(
    decoder.And(
        decoder.ByTopicCount(4),
        decoder.ByTopic0("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"),
    ),
    "ERC721",
    erc721Decoder,
)

// Specific DEX contracts
router.Register(
    decoder.ByAddresses([]string{"0xUniswapV2", "0xSushiSwap"}),
    "DEX",
    dexDecoder,
)

// Register with processor
processor.AddChain(chainInfo, options, router)
```

### Multi-Chain Indexing

```go
// Create processor with shared PostgreSQL sink and metrics
processor := core.NewProcessor(metrics, sharedSink)

// Configure Ethereum mainnet with optimized settings
ethereumOpts := &core.Options{
    RangeSize:              1000,  // Larger batches for high-throughput chain
    FetcherConcurrency:     4,     // Match RPC provider limits
    StartBlock:             18000000,
    ConfirmationDepth:      12,    // Standard reorg protection
    EnableTimestamps:       true,
    Topics: [][]string{{
        "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", // Transfer
    }},
    FetchMode:                core.FetchModeLogs,
    UseLogsForHistoricalSync: true,
    RetryConfig: &core.RetryConfig{
        MaxAttempts:    5,
        InitialBackoff: 5 * time.Second,
        MaxBackoff:     60 * time.Second,
        Multiplier:     2.0,
        EnableJitter:   true,
    },
}

ethereumChain := core.ChainInfo{
    ChainId: "1",
    Name:    "Ethereum",
    RPC:     core.NewHTTPRPC("https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY", 20, 5),
}

// Configure Polygon with chain-appropriate settings
polygonOpts := &core.Options{
    RangeSize:              2000,  // Larger batches for faster catch-up
    FetcherConcurrency:     2,     // Conservative for smaller chain
    StartBlock:             40000000,
    ConfirmationDepth:      100,   // Higher for faster finality
    EnableTimestamps:       true,
    Topics: [][]string{{
        "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
    }},
    FetchMode:                core.FetchModeLogs,
    UseLogsForHistoricalSync: true,
}

polygonChain := core.ChainInfo{
    ChainId: "137",
    Name:    "Polygon",
    RPC:     core.NewHTTPRPC("https://polygon-rpc.com", 15, 3),
}

// Create decoder routers for multi-contract support
ethereumRouter := createEthereumRouter()
polygonRouter := createPolygonRouter()

// Register chains with their respective routers
processor.AddChain(ethereumChain, ethereumOpts, ethereumRouter)
processor.AddChain(polygonChain, polygonOpts, polygonRouter)

// Start concurrent indexing with graceful shutdown
ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer cancel()

if err := processor.Run(ctx); err != nil && err != context.Canceled {
    log.Fatal(err)
}

func createEthereumRouter() *decoder.DecoderRouter {
    erc20Decoder := decoder.NewStandardDecoder()
    erc20Decoder.RegisterABI("ERC20", erc20ABI)

    erc721Decoder := decoder.NewStandardDecoder()
    erc721Decoder.RegisterABI("ERC721", erc721ABI)

    return decoder.NewDecoderRouter().
        Register(decoder.ByTopicCount(3), "ERC20", erc20Decoder).
        Register(decoder.ByTopicCount(4), "ERC721", erc721Decoder)
}
```

## Configuration

### Processor Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `RangeSize` | `int` | Required | Blocks per fetch batch (100-1000 recommended based on event density) |
| `FetcherConcurrency` | `int` | Required | Number of concurrent RPC fetch workers (match provider rate limits) |
| `StartBlock` | `uint64` | 0 | Starting block height (0 = resume from stored cursor) |
| `ConfirmationDepth` | `uint64` | Required | Blocks to wait before processing (12 for Ethereum, 100+ for faster chains) |
| `EnableTimestamps` | `bool` | `false` | Include block timestamps in events (increases RPC overhead) |
| `Topics` | `[][]string` | Required | Event signature hashes with OR logic support |
| `Addresses` | `[]string` | Optional | Contract addresses to monitor (empty = all addresses) |
| `FetchMode` | `FetchMode` | `FetchModeLogs` | `FetchModeLogs` (efficient) or `FetchModeReceipts` (comprehensive) |
| `ReorgLookbackBlocks` | `uint64` | 64 | Maximum blocks to examine during reorg ancestor search |
| `UseLogsForHistoricalSync` | `bool` | `true` | Prefer `eth_getLogs` for historical data fetching |
| `RetryConfig` | `*RetryConfig` | Default | Exponential backoff configuration for transient failures |

### RPC Configuration

```go
// NewHTTPRPC creates a rate-limited JSON-RPC client
rpc := core.NewHTTPRPC(
    "https://your-rpc-endpoint.com",  // RPC endpoint URL
    20,                               // Requests per second
    5,                                // Burst capacity
)
```

### Logging Configuration

```go
// Configure structured JSON logging
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,  // Debug, Info, Warn, Error
}))
processor.SetLogger(logger)
```

## Architecture

godex implements a high-performance producer-consumer pipeline with concurrent fetching, ordered processing, and fault-tolerant storage designed for production blockchain indexing.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                               Processor Core                                        │
│  ┌────────────┐  ┌────────────┐  ┌─────────────┐  ┌─────────────────────────────┐ │
│  │  Fetchers  │──│   Arbiter  │──│ Decoder     │──│           Sink             │ │
│  │ (Concurrent│  │ (Sequenced │  │ Router      │  │ (PostgreSQL, etc.)        │ │
│  │   RPC)     │  │   Queue)   │  │ (Routing)   │  │                             │ │
│  └────────────┘  └────────────┘  └─────────────┘  └─────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                            Per-Chain Processing                                    │
│  ┌────────────┐  ┌────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐ │
│  │ Chain 1    │  │ Chain 2    │  │ Chain N     │  │ Metrics     │  │ Logging     │ │
│  │ State      │  │ State      │  │ State       │  │ Collection  │  │ & Health    │ │
│  └────────────┘  └────────────┘  └─────────────┘  └─────────────┘  └─────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Core Components

| Component | Responsibility | Implementation |
|-----------|----------------|----------------|
| **Processor** | Orchestrates multi-chain indexing lifecycle with error isolation | Concurrent per-chain processing with shared resources |
| **Fetchers** | Concurrent RPC workers fetching logs and timestamps | Rate-limited batch requests with individual timeouts and retry logic |
| **Arbiter** | Maintains block order and coordinates processing pipeline | LRU cache for reorg detection, bounded buffering with context cancellation |
| **Decoder Router** | Intelligent event routing to appropriate decoders | Match-based routing with support for multiple contract types |
| **Sink** | Persistent storage with atomic rollback support | Transactional writes with reorg recovery and cursor management |

## Core Interfaces

### Sink Interface

Persistent storage abstraction with transactional rollback support.

```go
type Sink interface {
    // Store persists a batch of events atomically
    Store(ctx context.Context, events []types.Event) error

    // Rollback removes events from specified block onwards during reorgs
    Rollback(ctx context.Context, chainId string, toBlock uint64) error

    // LoadCursor retrieves the last processed block for resumption
    LoadCursor(ctx context.Context, chainId string) (blockNum uint64, blockHash string, err error)
}
```

### Decoder Interface

Event transformation with pluggable ABI support.

```go
type Decoder interface {
    // Decode transforms a single log into a structured event
    // name: ABI identifier, chainId: blockchain network identifier
    Decode(name string, chainId string, log types.Log) (*types.Event, error)

    // DecodeBatch processes multiple logs efficiently (optional optimization)
    DecodeBatch(logs []types.Log) (*[]types.Event, error)
}
```

### DecoderRouter Interface

Intelligent routing of logs to appropriate decoders based on configurable conditions.

```go
type DecoderRouter struct {
    routes []DecoderRoute
}

// Create new router instance
func NewDecoderRouter() *DecoderRouter

// Register decoder with match condition
func (r *DecoderRouter) Register(match MatchFunc, abiName string, dec Decoder) *DecoderRouter

// Implements Decoder interface with intelligent routing
func (r *DecoderRouter) Decode(chainId string, log types.Log) (*types.Event, error)
```

### Match Functions

Configurable conditions for routing logs to decoders.

```go
type MatchFunc func(log types.Log) bool

// Topic count matching
func ByTopicCount(count int) MatchFunc

// Address-based matching
func ByAddress(address string) MatchFunc
func ByAddresses(addresses []string) MatchFunc

// Event signature matching
func ByTopic0(topic0 string) MatchFunc

// Logical combinations
func And(matchers ...MatchFunc) MatchFunc
func Or(matchers ...MatchFunc) MatchFunc
```

### RPC Interface

Blockchain node communication with batching and rate limiting.

```go
type RPC interface {
    // Head retrieves the latest block number
    Head(ctx context.Context) (string, error)

    // GetBlock fetches a single block header
    GetBlock(ctx context.Context, blockNumber string) (types.Block, error)

    // GetBlocks fetches multiple blocks in a single batch request
    GetBlocks(ctx context.Context, blockNumbers []string) (map[string]types.Block, error)

    // GetLogs retrieves logs matching filter criteria
    GetLogs(ctx context.Context, filter types.Filter) ([]types.Log, error)

    // GetBlockReceipts fetches transaction receipts for a block
    GetBlockReceipts(ctx context.Context, blockNumber string) ([]types.Receipt, error)
}
```

## Reorganization Handling

Blockchain reorganizations are automatically detected and resolved with minimal data loss and efficient recovery.

**Detection Mechanism:**
1. Maintains LRU cache of processed block hashes (`BlockHashCache`)
2. Verifies parent hash continuity during sequential window processing
3. Detects divergence when `block.ParentHash != cachedHash[block.Number-1]`

**Recovery Process:**
1. **Ancestor Search**: Binary search backward through cached hashes to find common ancestor
2. **Sink Rollback**: Call `sink.Rollback(chainId, ancestor)` to remove orphaned events atomically
3. **State Reset**: Update cursor to ancestor block and clear future hash cache entries
4. **Resume Processing**: Restart from ancestor + 1 with fresh batch processing

**Performance Characteristics:**
- O(1) hash lookups via LRU cache
- Bounded memory usage with configurable cache size
- Minimal RPC overhead (only fetches headers during reorg detection)

**Configuration Options:**
- `ReorgLookbackBlocks`: Maximum blocks to examine (default: 64, balances detection range vs memory)
- `ConfirmationDepth`: Blocks to wait before processing (higher = fewer reorgs but increased latency)

**Monitoring Reorgs:**
```go
// Metrics collection includes reorg tracking
metrics.IncReorgs(chainId)  // Track reorg frequency
// Use metrics to adjust ConfirmationDepth based on chain behavior
```

## Observability & Metrics

Optional metrics collection for monitoring indexer performance and health.

```go
type Metrics interface {
    // Block processing metrics
    IncBlocksProcessed(chainId string, count uint64)
    ObservedBlockLag(chainId string, blocks uint64)
    ObservedBlockFetchDuration(chainId string, duration time.Duration, success bool)

    // Storage metrics
    IncSinkWrites(chainId string, count uint64)
    IncSinkErrors(chainId string)
    ObservedSinkWriteDuration(chainId string, duration time.Duration, success bool)

    // Indexer state
    SetIndexedHeight(chainId string, height uint64)
    SetProcessorConcurrency(chainId string, workers uint64)
    IncReorgs(chainId string)
}
```

**Implementation:** A Prometheus adapter is available at `adapters/metrics/prometheus.go`.

## Adapters

### PostgreSQL Sink

Production-ready event storage with transactional rollback support.

```go
import "github.com/ryuux05/godex/adapters/sink/postgres"

sink, err := postgres.NewPostgresSink(ctx, "postgres://user:pass@host:5432/db")
if err != nil {
    log.Fatal(err)
}
```

### Prometheus Metrics

Standard observability integration for monitoring and alerting.

```go
import "github.com/ryuux05/godex/adapters/metrics"

metrics := metrics.NewPrometheusMetrics()
processor := core.NewProcessor(metrics, sink)
```

## Performance Optimization

### Configuration Tuning

| Parameter | Impact | Recommendation |
|-----------|--------|----------------|
| `FetcherConcurrency` | Throughput vs rate limits | Match provider QPS limits (e.g., 20-50 for Alchemy) |
| `RangeSize` | Batch efficiency vs memory | 100-1000 blocks based on event density and reorg tolerance |
| `ConfirmationDepth` | Safety vs latency | 12 for Ethereum, 100+ for Polygon/Arbitrum |
| `FetchMode` | Performance vs reliability | `FetchModeLogs` for efficiency, `FetchModeReceipts` for completeness |
| `ReorgLookbackBlocks` | Detection range vs memory | 64-256 based on expected reorg depth |

### Fetch Modes

- **`FetchModeLogs`**: Uses `eth_getLogs` - most efficient for indexed events, may miss uncle blocks
- **`FetchModeReceipts`**: Uses `eth_getBlockReceipts` - comprehensive but higher RPC overhead
- **`UseLogsForHistoricalSync`**: Prefer `eth_getLogs` for initial historical sync to reduce costs

### RPC Optimization

- **Batch Requests**: Automatic batching for timestamp fetching reduces RPC calls by up to 90%
- **Rate Limiting**: Configurable QPS and burst limits prevent provider throttling
- **Exponential Backoff**: Jittered backoff prevents thundering herd on transient failures
- **Individual Timeouts**: Per-request timeouts prevent indefinite blocking (30s default)
- **Retry Logic**: Configurable retry attempts with smart failure classification

### Memory Management

- **Bounded Caching**: LRU block hash cache prevents unbounded memory growth
- **Window Processing**: Natural backpressure limits concurrent memory usage
- **Channel Buffering**: Sized channels provide backpressure without excessive queuing
- **Resource Cleanup**: Context cancellation ensures prompt resource release

### Monitoring & Observability

```go
// Key metrics for performance monitoring
processor := core.NewProcessor(prometheusMetrics, sink)

// Monitor these metrics:
// - Blocks processed per second
// - RPC request latency and success rates
// - Reorg frequency and recovery time
// - Memory usage and cache hit rates
// - Per-chain indexing progress
```

## Error Handling & Resilience

The SDK implements comprehensive error classification and handling for production reliability:

### Error Classification

- **Transient Errors**: Network timeouts, RPC rate limits - automatic retry with exponential backoff
- **Permanent Errors**: Invalid configuration, authentication failures - immediate chain termination
- **Reorg Errors**: Blockchain reorganizations - graceful rollback and recovery from ancestor
- **Non-Recoverable Errors**: Corrupted data, schema mismatches - chain isolation and logging

### Failure Isolation

- **Chain Independence**: Individual chain failures do not affect concurrent chain processing
- **Resource Cleanup**: Context cancellation ensures prompt cleanup of goroutines and connections
- **Graceful Degradation**: Failed chains log errors while others continue processing
- **Atomic Operations**: Sink operations use transactions with automatic rollback on failures

### Retry Configuration

```go
retryConfig := &core.RetryConfig{
    MaxAttempts:       3,                // Total attempts (including initial)
    InitialBackoff:    1 * time.Second,  // Starting delay
    MaxBackoff:        30 * time.Second, // Maximum delay
    Multiplier:        2.0,              // Exponential growth
    EnableJitter:      true,             // Randomize delays to prevent thundering herd
    PerRequestTimeout: 10 * time.Second, // Individual request timeout
}
```

### Monitoring Errors

```go
// Structured error logging
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))

// Errors are categorized and logged with context:
// - Chain ID, error type, retry attempts
// - RPC endpoint, request details
// - Processing state and recovery actions
```

## License

See [LICENSE](LICENSE) file.
