# godex

A high-performance blockchain indexing SDK written in Go for building scalable EVM-compatible blockchain indexers with automatic reorganization handling, multi-chain support, and structured event storage.

## Features

- **Multi-Chain Indexing**: Process events across multiple EVM-compatible chains concurrently
- **Automatic Reorganization Handling**: Built-in detection and rollback for blockchain reorganizations
- **Integrated Event Storage**: Events are automatically decoded and persisted to configurable sinks
- **Flexible Event Decoding**: Pluggable decoders transform raw logs into structured events
- **High Performance**: Concurrent fetching with configurable worker pools and batch RPC requests
- **Optional Timestamps**: Attach block timestamps to events with minimal RPC overhead
- **Production Ready**: Structured logging, comprehensive error handling, and observability metrics

## Installation

```bash
go get github.com/ryuux05/godex
```

## Quick Start

### Basic Indexer Setup

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

    // Initialize persistent storage sink
    sink, err := postgres.NewPostgresSink(context.Background(), "postgres://user:pass@localhost/db")
    if err != nil {
        log.Fatal(err)
    }

    // Configure indexing options
    opts := &core.Options{
        RangeSize:          1000,  // Blocks per batch
        FetcherConcurrency: 4,     // Concurrent fetch workers
        StartBlock:         18000000,
        ConfirmationDepth:  12,    // Wait for confirmations
        EnableTimestamps:   true,  // Include block timestamps
        Topics: []string{
            "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", // Transfer
        },
        FetchMode:              core.FetchModeLogs,
        UseLogsForHistoricalSync: true,
    }

    // Define chain configuration
    chain := core.ChainInfo{
        ChainId: "1",
        Name:    "Ethereum",
        RPC:     rpc,
    }

    // Initialize and configure decoder
    dec := decoder.NewStandardDecoder()
    dec.RegisterABI("ERC20", erc20ABI)

    // Create processor with optional metrics and required sink
    processor := core.NewProcessor(nil, sink)

    // Configure structured logging
    logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
    }))
    processor.SetLogger(logger)

    // Register chain with decoder
    if err := processor.AddChain(chain, opts, dec); err != nil {
        log.Fatal(err)
    }

    // Start continuous indexing
    ctx := context.Background()
    if err := processor.Run(ctx); err != nil {
        log.Fatal(err)
    }
}
```

### Multi-Chain Indexing

```go
// Create processor with shared sink
processor := core.NewProcessor(metrics, sharedSink)

// Configure Ethereum chain
ethereumOpts := &core.Options{
    RangeSize:          1000,
    FetcherConcurrency: 4,
    StartBlock:         18000000,
    ConfirmationDepth:  12,
    EnableTimestamps:   true,
    Topics: []string{
        "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
    },
}

ethereumChain := core.ChainInfo{
    ChainId: "1",
    Name:    "Ethereum",
    RPC:     ethereumRPC,
}

ethereumDecoder := decoder.NewStandardDecoder()
ethereumDecoder.RegisterABI("ERC20", erc20ABI)

// Configure Polygon chain
polygonOpts := &core.Options{
    RangeSize:          2000,  // Different batch size
    FetcherConcurrency: 2,     // Fewer workers for smaller chain
    StartBlock:         40000000,
    ConfirmationDepth:  100,   // Different confirmation requirements
    EnableTimestamps:   true,
    Topics: []string{
        "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
    },
}

polygonChain := core.ChainInfo{
    ChainId: "137",
    Name:    "Polygon",
    RPC:     polygonRPC,
}

polygonDecoder := decoder.NewStandardDecoder()
polygonDecoder.RegisterABI("ERC20", polygonERC20ABI)

// Register chains
processor.AddChain(ethereumChain, ethereumOpts, ethereumDecoder)
processor.AddChain(polygonChain, polygonOpts, polygonDecoder)

// Start concurrent indexing across all chains
processor.Run(ctx)
```

## Configuration

### Processor Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `RangeSize` | `int` | - | Blocks per fetch batch (balances latency vs throughput) |
| `BatchSize` | `int` | - | Events buffered before sink writes (unused, reserved for future) |
| `FetcherConcurrency` | `int` | - | Concurrent RPC fetch workers (bounded by rate limits) |
| `StartBlock` | `uint64` | 0 | Starting block (0 = resume from cursor or genesis) |
| `EndBlock` | `uint64` | 0 | Ending block (0 = continuous indexing) |
| `ConfirmationDepth` | `uint64` | - | Blocks to wait before processing (prevents reorgs) |
| `EnableTimestamps` | `bool` | `false` | Fetch block timestamps (additional RPC calls) |
| `ReorgLookbackBlocks` | `uint64` | 64 | Maximum blocks to walk back during reorg detection |
| `Topics` | `[]string` | - | Event signature hashes to filter |
| `Addresses` | `[]types.Address` | - | Contract addresses to monitor |
| `FetchMode` | `FetchMode` | `"logs"` | `"logs"` (efficient) or `"receipts"` (reliable) |
| `UseLogsForHistoricalSync` | `bool` | `true` | Use `eth_getLogs` for historical data |
| `RetryConfig` | `*rpc.RetryConfig` | default | Exponential backoff settings |

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

godex implements a producer-consumer pattern with concurrent fetching, ordered processing, and fault-tolerant storage.

```
┌─────────────────────────────────────────────────────────────────────┐
│                          Processor                                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌─────────────────────┐ │
│  │ Fetchers │──│  Arbiter │──│ Decoder  │──│       Sink          │ │
│  │ (RPC)    │  │ (Ordered │  │ (Events) │  │ (PostgreSQL, etc.)  │ │
│  │          │  │  Queue)  │  │          │  │                     │ │
│  └──────────┘  └──────────┘  └──────────┘  └─────────────────────┘ │
└─────────────────────────────────────────────────────────────────────┘
```

### Core Components

| Component | Responsibility | Implementation |
|-----------|----------------|----------------|
| **Processor** | Orchestrates indexing lifecycle and manages per-chain state | Concurrent chain processing with error isolation |
| **Fetchers** | Concurrent RPC workers fetching logs and timestamps | Rate-limited batch requests with retry logic |
| **Arbiter** | Maintains block order and coordinates processing pipeline | LRU cache for reorg detection, bounded buffering |
| **Decoder** | Transforms raw logs into structured events | Pluggable ABI-based decoding with error resilience |
| **Sink** | Persistent storage with atomic rollback support | Transactional writes with reorg recovery |

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
    Decode(name string, chainId string, log types.Log) (*types.Event, error)

    // DecodeBatch processes multiple logs efficiently
    DecodeBatch(logs []types.Log) (*[]types.Event, error)
}
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

Blockchain reorganizations are automatically detected and resolved to maintain data consistency.

**Detection Process:**
1. Compares parent block hashes during sequential processing
2. Identifies divergence using an LRU cache of recent block hashes
3. Performs binary search to locate the common ancestor block
4. Rolls back orphaned events via `Sink.Rollback()`
5. Resumes processing from the confirmed ancestor

**Configuration:**
- `ReorgLookbackBlocks`: Maximum blocks to examine during ancestor search (default: 64)
- `ConfirmationDepth`: Blocks to wait before processing to avoid most reorgs

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
| `FetcherConcurrency` | RPC throughput vs rate limits | Match your provider's QPS limits |
| `RangeSize` | Batch efficiency vs memory | 100-1000 blocks based on event density |
| `ConfirmationDepth` | Reorg safety vs latency | 12 for Ethereum, 100+ for smaller chains |
| `FetchMode` | Performance vs reliability | `"logs"` for efficiency, `"receipts"` for completeness |

### Fetch Modes

- **`FetchModeLogs`**: Uses `eth_getLogs` - efficient but may miss uncle blocks
- **`FetchModeReceipts`**: Uses `eth_getBlockReceipts` - reliable but higher bandwidth
- **`UseLogsForHistoricalSync`**: Prefer `eth_getLogs` for historical data to reduce costs

### RPC Optimization

- Batch requests automatically used for timestamp fetching
- Exponential backoff with jitter prevents thundering herd
- Rate limiting prevents provider quota exhaustion

## Error Handling

The SDK implements comprehensive error handling:

- **Transient errors**: Automatic retry with exponential backoff
- **Permanent errors**: Immediate failure with detailed logging
- **Reorg detection**: Graceful rollback and recovery
- **Resource limits**: Bounded memory usage with channel backpressure

## License

See [LICENSE](LICENSE) file.
