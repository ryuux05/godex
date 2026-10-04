# godex

A blockchain indexing SDK written in Go for building scalable EVM-compatible blockchain indexers. Features automatic reorganization handling, intelligent multi-contract event routing, concurrent multi-chain processing, and structured event persistence.

## Features

- **Concurrent Multi-Chain Indexing**: Process events across multiple EVM-compatible chains simultaneously
- **Automatic Reorganization Handling**: Built-in detection and rollback for blockchain reorganizations
- **Intelligent Event Routing**: DecoderRouter enables complex multi-contract scenarios
- **High-Performance Processing**: Concurrent fetching with configurable worker pools and batch RPC requests
- **Transactional Storage**: Transactional event persistence with atomic rollback support
- **Comprehensive Observability**: Structured logging, metrics collection, and health monitoring

## Installation

```bash
go get github.com/ryuux05/godex
```

## Quick Start

### Basic Setup

Set `DATABASE_URL`, `RPC_URL`, and `CONTRACT_ADDRESS`. This example stores
selected decoded events in PostgreSQL and resumes stored progress:

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/ryuux05/godex/pkg/godex"
)

const erc20ABI = `[{"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256"}]}]`

func main() {
    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()
    pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
    if err != nil { log.Fatal(err) }
    defer pool.Close()

    indexer, err := godex.New(ctx, godex.Config{
        ChainID: "1",
        RPCURL: os.Getenv("RPC_URL"),
        Postgres: &godex.PostgresConfig{Pool: pool},
        Contracts: []godex.Contract{{
            Address: os.Getenv("CONTRACT_ADDRESS"), ABI: erc20ABI,
            Events: []string{"Transfer"},
        }},
        FromBlock: 18_000_000, // First indexed block in a fresh database.
        ConfirmationDepth: 12,
    })
    if err != nil { log.Fatal(err) }
    if err := indexer.Run(ctx); err != nil { log.Fatal(err) }
}
```

`New` derives topic filters and decoder routes from the ABI, checks the RPC chain
ID before storage access, and applies documented tuning defaults. Supplied pools
and HTTP clients remain caller-owned. Use an existing `Sink` instead of
`Postgres` when integrating another backend.

For function handlers, transactional rollback, safe event accessors, custom HTTP
clients, and tuning, see the [service integration guide](docs/sdk.md). A runnable
example is available with `go run ./examples/service`. The lower-level
`NewProcessor` and `AddChainContext` APIs remain available.

## Configuration

### Processor Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `RangeSize` | `int` | Required | Blocks per batch (see tuning guide below) |
| `FetcherConcurrency` | `int` | Required | Concurrent RPC workers (see tuning guide below) |
| `StartBlock` | `uint64` | 0 | Initial cursor; indexing begins at StartBlock + 1 (0 = resume) |
| `ConfirmationDepth` | `uint64` | Required | Blocks to wait before processing |
| `EnableTimestamps` | `bool` | `false` | Include block timestamps (increases RPC calls) |
| `Topics` | `[][]string` | Required | Event signature hashes to filter |
| `Addresses` | `[]string` | Optional | Contract addresses to monitor |
| `FetchMode` | `FetchMode` | `FetchModeLogs` | `FetchModeLogs` or `FetchModeReceipts` |
| `ReorgLookbackBlocks` | `uint64` | 64 | Max blocks for reorg ancestor search |
| `PollInterval` | `time.Duration` | 1s | Head polling delay when caught up |
| `MaxInFlightRanges` | `int` | 2 × workers | Maximum fetched ranges awaiting ordered commits |
| `SkipDecodeErrors` | `bool` | `false` | Opt in to discarding logs with decode errors |
| `RetryConfig` | `*RetryConfig` | Default | Retry configuration |

### RPC Configuration

```go
rpc := godex.NewHTTPRPC(
    "https://your-rpc-endpoint.com",
    20,  // Requests per second (match provider limits)
    5,   // Burst capacity
)
```

### Retry Configuration

```go
retryConfig := &godex.RetryConfig{
    MaxAttempts:       3,
    InitialBackoff:    1 * time.Second,
    MaxBackoff:        30 * time.Second,
    Multiplier:        2.0,
    EnableJitter:      true,
    PerRequestTimeout: 10 * time.Second,
}
```

## Configuration Tuning Guide

### Finding the Right Balance

#### FetcherConcurrency

**Too Low:**
- Underutilized RPC provider capacity
- Slower indexing speed
- Symptoms: Low blocks/second, RPC calls not hitting rate limits

**Too High:**
- Rate limit errors (429)
- Provider throttling
- Symptoms: Frequent retries, "rate limit exceeded" errors

**Recommended:**
- Start with provider's documented QPS limit
- Monitor rate limit errors and adjust down if needed
- Example: Alchemy (20-50), Infura (10-20), Public RPC (5-10)

#### RangeSize

**Too Small (< 100):**
- High RPC overhead
- Many small transactions
- Symptoms: High RPC call count, slow progress

**Too Large (> 2000):**
- May exceed provider block range limits
- May exceed RPC response size limits (5-10MB typical)
- Higher memory usage
- Larger rollback scope on reorgs
- Symptoms: RPC errors ("response too large"), memory spikes

**Recommended:**
- **Historical sync**: 500-2000 blocks (faster catch-up, watch for response size limits)
- **Live sync**: 100-500 blocks (lower latency)
- **High event density**: 100-500 blocks (manage memory and response size)
- **Low event density**: 500-1000 blocks (efficiency)
- **If hitting response size limits**: Reduce to 50-200 blocks

#### ConfirmationDepth

**Too Low:**
- Frequent reorgs detected
- More rollback operations
- Symptoms: High reorg count, frequent cursor updates

**Too High:**
- Delayed event availability
- Slower indexing progress
- Symptoms: Events appear late, slow block advancement

**Recommended:**
- **Ethereum**: 12 blocks (PoS finality)
- **Polygon/Arbitrum**: 100+ blocks (faster finality)
- **BSC**: 15 blocks
- **Optimism**: 12 blocks

#### FetchMode

**FetchModeLogs:**
- Most efficient for indexed events
- Lower RPC cost
- May miss uncle blocks
- **Use for**: Most scenarios, historical sync

**FetchModeReceipts:**
- Comprehensive (includes all transactions)
- Higher RPC overhead
- More reliable for contract-specific indexing
- **Use for**: Targeted contract monitoring, when completeness is critical

### Performance Tuning Checklist

1. **Monitor RPC rate limits**: Adjust `FetcherConcurrency` if hitting limits
2. **Check block processing speed**: Increase `RangeSize` if too slow
3. **Monitor reorg frequency**: Increase `ConfirmationDepth` if too many reorgs
4. **Watch memory usage**: Decrease `RangeSize` if memory spikes
5. **Review retry frequency**: Adjust `RetryConfig` if too many retries

## Common Problems and Solutions

### Problem: Rate Limit Errors (429)

**Symptoms:**
- Frequent "rate limit exceeded" errors
- High retry count in logs
- Slow indexing progress

**Solutions:**
1. **Reduce FetcherConcurrency**: Lower to 50-75% of provider limit
2. **Increase burst capacity**: Set burst to 20-30% of rate limit
3. **Check provider limits**: Verify you're not exceeding plan limits
4. **Use multiple RPC endpoints**: Distribute load across providers

```go
// Example: Reduce concurrency
opts.FetcherConcurrency = 10  // Down from 20
```

### Problem: RPC Response Too Large

**Symptoms:**
- RPC errors about response size limits
- "response too large" or "result exceeds limit" errors
- Fetches failing for large block ranges
- Errors when processing blocks with many events

**Automatic Handling:**
The SDK automatically handles "response too big" errors (typically error code `-32008`) by recursively splitting the block range into smaller chunks until the response size is acceptable. This happens transparently during normal operation - you'll see log messages like "too big response occur, split request" when this occurs.

**Important:** If a single block returns a "response too big" error, this indicates a fundamental problem with the RPC endpoint itself, not the request. In such cases:
- **Switch to a different RPC provider** with higher response size limits
- **Reduce the number of events** being indexed (narrow `Topics` or `Addresses` filters)
- **Use a dedicated RPC node** with custom limits

**Manual Solutions:**
1. **Reduce RangeSize**: Smaller block ranges produce smaller responses (prevents splitting overhead)
2. **Use RPC provider with larger limits**: Some providers support bigger responses
3. **Filter more aggressively**: Use address filters to reduce event count
4. **Switch to FetchModeReceipts**: May have different size limits (though less efficient)

```go
// Example: Reduce range size to avoid automatic splitting
opts.RangeSize = 100  // Down from 1000 to avoid large responses

// Or use provider with larger limits
rpc := godex.NewHTTPRPC("https://provider-with-larger-limits.com", 20, 5)
```

**Provider Response Size Limits:**
- **Alchemy**: ~10MB response limit
- **Infura**: ~5MB response limit
- **Public RPC**: Varies, often lower
- **Self-hosted**: Configurable (check node settings)

**When to Reduce RangeSize:**
- High event density blocks (many events per block)
- Large event data (complex events with large data fields)
- Multiple contracts emitting events in same range

### Problem: Slow Indexing Speed

**Symptoms:**
- Low blocks/second rate
- Indexer falling behind chain head
- High block lag

**Solutions:**
1. **Increase FetcherConcurrency**: Up to provider's rate limit
2. **Increase RangeSize**: Larger batches reduce overhead
3. **Use FetchModeLogs**: More efficient than receipts
4. **Enable UseLogsForHistoricalSync**: Faster historical catch-up
5. **Check RPC latency**: Switch to faster/closer RPC endpoint

```go
// Example: Optimize for speed
opts.FetcherConcurrency = 20  // Increase workers
opts.RangeSize = 2000         // Larger batches
opts.FetchMode = godex.FetchModeLogs
opts.UseLogsForHistoricalSync = true
```

### Problem: High Memory Usage

**Symptoms:**
- Memory growing over time
- OOM errors
- System slowdown

**Solutions:**
1. **Reduce RangeSize**: Smaller batches use less memory
2. **Reduce FetcherConcurrency**: Fewer concurrent operations
3. **Check ReorgLookbackBlocks**: Lower if too high (default 64 is usually fine)
4. **Monitor channel buffering**: Ensure backpressure is working

```go
// Example: Reduce memory usage
opts.RangeSize = 500          // Smaller batches
opts.FetcherConcurrency = 4  // Fewer workers
opts.ReorgLookbackBlocks = 64 // Keep default
```

### Problem: Frequent Reorganizations

**Symptoms:**
- High reorg count in metrics
- Frequent rollback operations
- Cursor frequently updated backward

**Solutions:**
1. **Increase ConfirmationDepth**: Wait more blocks before processing
2. **Monitor chain stability**: Some chains have more reorgs
3. **Check ReorgLookbackBlocks**: Ensure sufficient lookback range

```go
// Example: Reduce reorgs
opts.ConfirmationDepth = 20   // Up from 12 for Ethereum
// For faster chains:
opts.ConfirmationDepth = 200  // Polygon/Arbitrum
```

### Problem: Context Canceled Errors

**Symptoms:**
- "context canceled" errors in logs
- Indexer stops unexpectedly
- Premature shutdown

**Solutions:**
1. **Check timeout settings**: Ensure sufficient `PerRequestTimeout`
2. **Verify context propagation**: Don't cancel parent context prematurely
3. **Check graceful shutdown**: Use signal-based cancellation properly

```go
// Example: Increase timeouts
retryConfig.PerRequestTimeout = 30 * time.Second  // Up from 10s
```

### Problem: Sink Write Errors

**Symptoms:**
- Database connection errors
- Transaction failures
- Events not persisting

**Solutions:**
1. **Check database connection**: Verify connection string and pool size
2. **Monitor connection pool**: Ensure sufficient connections
3. **Check transaction size**: Reduce batch size if transactions too large
4. **Verify schema**: Ensure tables exist and migrations applied

```go
// Example: Optimize sink
sink, _ := postgres.NewSink(postgres.SinkConfig{
    Pool:          pool,
    Handler:       handler,
    CopyThreshold: 32,  // Use COPY for large batches
})
```

### Problem: Decoder Not Matching Logs

**Symptoms:**
- Events not decoded
- Warnings about failed decoding
- Zero events stored

**Solutions:**
1. **Verify ABI registration**: Ensure ABI includes all event definitions
2. **Check matcher logic**: Verify router matchers match your logs
3. **Verify topic filters**: Ensure Topics configuration matches events
4. **Check address filters**: Verify Addresses includes target contracts

```go
// Example: Debug decoder
router := decoder.NewDecoderRouter()
router.Register(
    decoder.ByAddress("0xYourContract"),  // Verify address
    "YourABI",
    decoder,
)
```

### Problem: Indexer Not Resuming from Cursor

**Symptoms:**
- Starts from StartBlock instead of cursor
- Duplicate events
- Lost progress

**Solutions:**
1. **Verify cursor exists**: Check `chronicle_cursors` table
2. **Check LoadCursor implementation**: Ensure sink loads cursor correctly
3. **Set StartBlock to 0**: Let processor use cursor when available

```go
// Example: Proper cursor usage
opts.StartBlock = 0  // Use cursor if available
```

## Monitoring and Health

### Status and Health Checks

```go
status := p.Status()
chain := status.Chains["1"]
fmt.Printf("Block: %d/%d (%.1f%%) - %.0f blk/s\n",
    chain.CursorBlock, chain.HeadBlock, chain.ProgressPct, chain.BlocksPerSec)

health := p.Health()
if !health.Healthy {
    log.Printf("Unhealthy: %v", health.Reasons)
}
```

### Metrics

Enable Prometheus metrics for monitoring:

```go
import "github.com/ryuux05/godex/adapters/metrics"

collector := metrics.New("godex", prometheus.DefaultRegisterer)
processor := godex.NewProcessor(collector, sink)

// Expose metrics endpoint
http.Handle("/metrics", promhttp.Handler())
```

**Key Metrics to Monitor:**
- `godex_block_processed_total` - Indexing progress
- `godex_block_lag` - How far behind chain head
- `godex_block_fetched_duration_seconds` - RPC performance
- `godex_sink_events_writes_total` - Storage throughput
- `godex_sink_events_errors_total` - Storage failures
- `godex_reorgs_total` - Reorg frequency

## Examples

- [Service Integration](examples/service/) - Configuration-driven SDK integration
- [ERC20 Indexer](examples/erc20-indexer/) - Complete example with PostgreSQL storage
- See [examples/](examples/) directory for more examples

## Documentation

- [Service Integration Guide](docs/sdk.md) - Simple setup, borrowed resources, and handler functions
- [Architecture Overview](docs/architecture.md) - System architecture and design principles
- [Processor Guide](docs/processor.md) - Processor configuration and behavior
- [Decoder Guide](docs/decoder.md) - Event decoding and routing
- [RPC Guide](docs/rpc.md) - RPC client configuration and optimization
- [Sink Guide](docs/sink.md) - Storage backends and persistence
- [Production Guide](docs/production.md) - Persistence contracts, deployment, and compatibility notes
- [Testing Guide](docs/testing.md) - Race detection, PostgreSQL integration tests, and fuzzing
- [Repository Review](docs/repository-review.md) - Coverage improvements and remaining priorities

## License

See [LICENSE](LICENSE) file.
