# godex

A high-performance blockchain indexing SDK written in Go, designed for building robust indexers that process EVM-compatible blockchain events with automatic reorg handling, multi-chain support, and persistent storage.

## Features

- **Multi-Chain Support**: Index events across multiple EVM-compatible chains simultaneously
- **Automatic Reorg Handling**: Built-in detection and rollback for blockchain reorganizations
- **Integrated Sink Storage**: Events are automatically decoded and stored to your configured sink
- **Flexible Event Decoding**: Custom decoders transform raw logs into structured events
- **High Performance**: Concurrent fetching with configurable worker pools and batch RPC requests
- **Optional Timestamps**: Fetch block timestamps for events with a single config flag
- **Production Ready**: Comprehensive error handling, retry mechanisms, and metrics support

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
    "log"
    
    "github.com/ryuux05/godex/pkg/core"
    "github.com/ryuux05/godex/pkg/core/decoder"
    "github.com/ryuux05/godex/adapters/sink/postgres"
)

func main() {
    // Initialize RPC client (endpoint, rateLimit, burstLimit)
    rpc := core.NewHTTPRPC("https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY", 20, 5)
    
    // Initialize sink (e.g., PostgreSQL)
    sink, err := postgres.NewPostgresSink(context.Background(), "postgres://...")
    if err != nil {
        log.Fatal(err)
    }
    
    // Configure processor options
    opts := &core.Options{
        RangeSize:          100,
        FetcherConcurrency: 4,
        StartBlock:         18000000,
        ConfimationDepth:   15,
        EnableTimestamps:   true,
        Topics: []string{
            "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", // Transfer
        },
        FetchMode: core.FetchModeLogs,
    }
    
    // Setup chain info
    chain := core.ChainInfo{
        ChainId: "1",
        Name:    "Ethereum",
        RPC:     rpc,
    }
    
    // Initialize decoder
    dec := decoder.NewStandardDecoder()
    dec.RegisterABI("ERC20", erc20ABI)
    
    // Create processor with metrics (optional) and sink
    processor := core.NewProcessor(nil, sink)
    
    // Add chain with options and decoder
    if err := processor.AddChain(chain, opts, dec); err != nil {
        log.Fatal(err)
    }
    
    // Start indexing - events are automatically decoded and stored to sink
    ctx := context.Background()
    if err := processor.Run(ctx); err != nil {
        log.Fatal(err)
    }
}
```

### Multi-Chain Indexing

```go
processor := core.NewProcessor(nil, sink)

// Add Ethereum chain
ethereumChain := core.ChainInfo{
    ChainId: "1",
    Name:    "Ethereum",
    RPC:     ethereumRPC,
}
processor.AddChain(ethereumChain, ethereumOpts, ethereumDecoder)

// Add Polygon chain
polygonChain := core.ChainInfo{
    ChainId: "137",
    Name:    "Polygon",
    RPC:     polygonRPC,
}
processor.AddChain(polygonChain, polygonOpts, polygonDecoder)

// Run all chains concurrently
processor.Run(ctx)
```

## Configuration

### Processor Options

| Option | Description |
|--------|-------------|
| `RangeSize` | Number of blocks to fetch per batch |
| `BatchSize` | Number of events to buffer before writing to sink |
| `FetcherConcurrency` | Number of concurrent RPC fetchers |
| `StartBlock` | Initial block number to start indexing |
| `EndBlock` | Optional block to stop indexing at (0 = run continuously) |
| `ConfimationDepth` | Number of confirmations before processing (avoids most reorgs) |
| `EnableTimestamps` | Fetch and attach block timestamps to events |
| `ReorgLookbackBlocks` | Maximum blocks to walk back when detecting reorgs (default: 64) |
| `Topics` | Event topic hashes to filter |
| `Addresses` | Contract addresses to filter |
| `FetchMode` | `FetchModeLogs` or `FetchModeReceipts` |
| `UseLogsForHistoricalSync` | Use eth_getLogs during historical sync (default: true) |
| `RetryConfig` | Configure retry behavior for RPC errors |

### RPC Configuration

```go
// NewHTTPRPC(endpoint, rateLimit, burstLimit)
rpc := core.NewHTTPRPC(
    "https://your-rpc-endpoint.com",
    20,  // Rate limit (requests per second)
    5,   // Burst limit (max concurrent requests)
)
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Processor                               │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────┐ │
│  │ Fetcher  │──│ Arbiter  │──│ Decoder  │──│      Sink        │ │
│  │ (RPC)    │  │ (Order)  │  │ (Events) │  │ (Postgres, etc.) │ │
│  └──────────┘  └──────────┘  └──────────┘  └──────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

### Components

| Component | Description |
|-----------|-------------|
| **Processor** | Orchestrates block fetching, log retrieval, and reorg detection |
| **Fetcher** | Concurrent workers that fetch logs from RPC (with optional timestamp fetching) |
| **Arbiter** | Ensures ordered processing and coordinates decode → store flow |
| **Decoder** | Transforms raw blockchain logs into structured events |
| **Sink** | Persists events to storage (PostgreSQL, etc.) |

## Interfaces

### Sink Interface

```go
type Sink interface {
    // Store persists events
    Store(ctx context.Context, events []types.Event) error
    // Rollback removes events from a block number onwards (for reorg handling)
    Rollback(ctx context.Context, chainId string, toBlock uint64) error
    // LoadCursor retrieves the last processed block for resumption
    LoadCursor(ctx context.Context, chainId string) (blockNum uint64, blockHash string, err error)
}
```

### Decoder Interface

```go
type Decoder interface {
    // Decode transforms a log into a structured event
    Decode(name string, chainId string, log types.Log) (*types.Event, error)
    // DecodeBatch for batch decoding
    DecodeBatch(logs []types.Log) (*[]types.Event, error)
}
```

### RPC Interface

```go
type RPC interface {
    Head(ctx context.Context) (string, error)
    GetBlock(ctx context.Context, blockNumber string) (types.Block, error)
    GetBlocks(ctx context.Context, blockNumbers []string) (map[string]types.Block, error)
    GetLogs(ctx context.Context, filter types.Filter) ([]types.Log, error)
    GetBlockReceipts(ctx context.Context, blockNumber string) ([]types.Receipt, error)
}
```

## Reorg Handling

The SDK automatically detects blockchain reorganizations by comparing parent block hashes:

1. Detects chain divergence via parent hash mismatch
2. Walks back to find the common ancestor block
3. Calls `Sink.Rollback()` to remove orphaned data
4. Resumes indexing from the ancestor block

## Metrics

Optional metrics interface for observability:

```go
type Metrics interface {
    IncBlocksProcessed(chain string)
    ObservedBlockLag(chain string, lag float64)
    ObservedBlockFetchDuration(chain string, d float64)
    SetIndexedHeight(chain string, h float64)
    IncSinkWrites(chain string)
    SetProcessorConcurrency(chain string, v float64)
    IncSinkErrors(chain string)
    ObservedSinkWriteDuration(chain string, d float64)
    IncReorgs(chain string)
}
```

A Prometheus adapter is available at `adapters/metrics/prometheus.go`.

## Adapters

### PostgreSQL Sink

```go
import "github.com/ryuux05/godex/adapters/sink/postgres"

sink, err := postgres.NewPostgresSink(ctx, connectionString)
```

### Prometheus Metrics

```go
import "github.com/ryuux05/godex/adapters/metrics"

m := metrics.NewPrometheusMetrics()
processor := core.NewProcessor(m, sink)
```

## Performance Considerations

- Use appropriate `FetcherConcurrency` based on your RPC rate limits
- Adjust `RangeSize` to balance between RPC call frequency and memory usage
- Use `FetchModeReceipts` for better performance when filtering by contract addresses
- Enable `UseLogsForHistoricalSync` to reduce RPC costs during historical sync
- `GetBlocks` uses JSON-RPC batch requests to optimize timestamp fetching

## License

See [LICENSE](LICENSE) file.
