# godex

A high-performance, production-ready blockchain indexing SDK written in Go for building scalable EVM-compatible blockchain indexers. Features automatic reorganization handling, intelligent multi-contract event routing, concurrent multi-chain processing, and structured event persistence.

## Features

- **Concurrent Multi-Chain Indexing**: Process events across multiple EVM-compatible chains simultaneously
- **Automatic Reorganization Handling**: Built-in detection and rollback for blockchain reorganizations
- **Intelligent Event Routing**: DecoderRouter enables complex multi-contract scenarios
- **High-Performance Processing**: Concurrent fetching with configurable worker pools and batch RPC requests
- **Production-Ready Storage**: Transactional event persistence with atomic rollback support
- **Comprehensive Observability**: Structured logging, metrics collection, and health monitoring

## Installation

```bash
go get github.com/ryuux05/godex
```

## Quick Start

### Basic Setup

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/ryuux05/godex/pkg/core"
    "github.com/ryuux05/godex/pkg/core/decoder"
    "github.com/ryuux05/godex/adapters/sink/postgres"
)

func main() {
    // Initialize RPC client
    rpc := core.NewHTTPRPC("https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY", 20, 5)

    // Initialize PostgreSQL sink
    pool, _ := pgxpool.New(context.Background(), "postgres://user:pass@localhost:5432/godex")
    handler := &MyEventHandler{}
    sink, _ := postgres.NewSink(postgres.SinkConfig{
        Pool:    pool,
        Handler: handler,
    })

    // Configure indexing options
    opts := &core.Options{
        RangeSize:          1000,
        FetcherConcurrency: 4,
        StartBlock:         18000000,
        ConfirmationDepth:  12,
        Topics: [][]string{{
            "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
        }},
    }

    // Setup decoder
    dec := decoder.NewStandardDecoder()
    dec.RegisterABI("ERC20", erc20ABI)  // Load ABI from file or embed

    // Create and run processor
    processor := core.NewProcessor(nil, sink)
    processor.SetLogger(slog.Default())

    processor.AddChain(core.ChainInfo{
        ChainId: "1",
        Name:    "Ethereum",
        RPC:     rpc,
    }, opts, dec)

    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    processor.Run(ctx)
}
```

## Configuration

### Processor Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `RangeSize` | `int` | Required | Blocks per batch (see tuning guide below) |
| `FetcherConcurrency` | `int` | Required | Concurrent RPC workers (see tuning guide below) |
| `StartBlock` | `uint64` | 0 | Starting block (0 = resume from cursor) |
| `ConfirmationDepth` | `uint64` | Required | Blocks to wait before processing |
| `EnableTimestamps` | `bool` | `false` | Include block timestamps (increases RPC calls) |
| `Topics` | `[][]string` | Required | Event signature hashes to filter |
| `Addresses` | `[]string` | Optional | Contract addresses to monitor |
| `FetchMode` | `FetchMode` | `FetchModeLogs` | `FetchModeLogs` or `FetchModeReceipts` |
| `ReorgLookbackBlocks` | `uint64` | 64 | Max blocks for reorg ancestor search |
| `RetryConfig` | `*RetryConfig` | Default | Retry configuration |

### RPC Configuration

```go
rpc := core.NewHTTPRPC(
    "https://your-rpc-endpoint.com",
    20,  // Requests per second (match provider limits)
    5,   // Burst capacity
)
```

### Retry Configuration

```go
retryConfig := &core.RetryConfig{
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

**Solutions:**
1. **Reduce RangeSize**: Smaller block ranges produce smaller responses
2. **Use RPC provider with larger limits**: Some providers support bigger responses
3. **Filter more aggressively**: Use address filters to reduce event count
4. **Switch to FetchModeReceipts**: May have different size limits (though less efficient)
5. **Process in smaller chunks**: Break large ranges into smaller batches

```go
// Example: Reduce range size
opts.RangeSize = 100  // Down from 1000 to avoid large responses

// Or use provider with larger limits
rpc := core.NewHTTPRPC("https://provider-with-larger-limits.com", 20, 5)
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
opts.FetchMode = core.FetchModeLogs
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
// Get chain status
status, err := processor.Status("1")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Block: %d/%d (%.1f%%) - %.0f blk/s\n",
    status.CurrentBlock, status.HeadBlock,
    status.ProgressPct, status.BlocksPerSec)

// Health check
health, err := processor.Health(ctx)
if err != nil {
    log.Fatal(err)
}
if !health.Healthy {
    log.Printf("Unhealthy: %v", health.Errors)
}
```

### Metrics

Enable Prometheus metrics for monitoring:

```go
import "github.com/ryuux05/godex/adapters/metrics"

metrics := metrics.NewPrometheusMetrics()
processor := core.NewProcessor(metrics, sink)

// Expose metrics endpoint
http.Handle("/metrics", promhttp.Handler())
```

**Key Metrics to Monitor:**
- `godex_blocks_processed_total` - Indexing progress
- `godex_block_lag` - How far behind chain head
- `godex_block_fetched_duration_seconds` - RPC performance
- `godex_sink_events_writes_total` - Storage throughput
- `godex_sink_events_errors_total` - Storage failures
- `godex_reorgs_total` - Reorg frequency

## Examples

- [ERC20 Indexer](examples/erc20-indexer/) - Complete example with PostgreSQL storage
- See [examples/](examples/) directory for more examples

## Documentation

- [Architecture Overview](docs/indexer_architecture.md) - System architecture and design principles
- [Processor Guide](docs/processor.md) - Processor configuration and behavior
- [Decoder Guide](docs/decoder.md) - Event decoding and routing
- [RPC Guide](docs/rpc.md) - RPC client configuration and optimization
- [Sink Guide](docs/sink.md) - Storage backends and persistence

## License

See [LICENSE](LICENSE) file.
