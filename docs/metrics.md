# Metrics & Observability

## Overview

The SDK provides comprehensive metrics collection for monitoring indexer performance, health, and throughput. Metrics enable observability, alerting, and performance optimization.

## Metrics Interface

```go
type Metrics interface {
    // Block processing counters
    IncBlocksProcessed(chainId string, count uint64)

    // Processing lag (head - cursor position)
    ObservedBlockLag(chainId string, blocks uint64)

    // RPC performance
    ObservedBlockFetchDuration(chainId string, duration time.Duration, success bool)

    // Storage performance and errors
    IncSinkWrites(chainId string, count uint64)
    IncSinkErrors(chainId string)
    ObservedSinkWriteDuration(chainId string, duration time.Duration, success bool)

    // Indexer state
    SetIndexedHeight(chainId string, height uint64)
    SetProcessorConcurrency(chainId string, workers uint64)
    IncReorgs(chainId string)
}
```

## Metric Types

### Counters
- **Blocks Processed**: Total blocks successfully indexed
- **Sink Writes**: Number of storage operations
- **Sink Errors**: Failed storage operations
- **Reorgs**: Blockchain reorganizations detected

### Gauges
- **Indexed Height**: Current processing position per chain
- **Block Lag**: Distance between chain head and indexer position
- **Processor Concurrency**: Active worker goroutines

### Histograms
- **Block Fetch Duration**: Time to fetch logs from RPC
- **Sink Write Duration**: Time to persist events

## Prometheus Implementation

The Prometheus adapter provides standard metric collection:

```go
import "github.com/ryuux05/godex/adapters/metrics"

metrics := metrics.NewPrometheusMetrics()
processor := core.NewProcessor(metrics, sink)
```

### Exported Metrics

| Metric Name | Type | Labels | Description |
|-------------|------|--------|-------------|
| `godex_blocks_processed_total` | Counter | `chain_id` | Total blocks indexed |
| `godex_block_lag` | Gauge | `chain_id` | Blocks behind chain head |
| `godex_reorgs_total` | Counter | `chain_id` | Reorganizations detected |
| `godex_sink_writes_total` | Counter | `chain_id` | Storage operations |
| `godex_sink_errors_total` | Counter | `chain_id` | Storage failures |
| `godex_indexed_height` | Gauge | `chain_id` | Current block position |
| `godex_processor_concurrency` | Gauge | `chain_id` | Active workers |
| `godex_block_fetch_duration_seconds` | Histogram | `chain_id`, `success` | RPC fetch latency |
| `godex_sink_write_duration_seconds` | Histogram | `chain_id`, `success` | Storage latency |

## Usage Examples

### Monitoring Health

```prometheus
# Alert if indexer is falling behind
godex_block_lag{chain_id="1"} > 100

# Alert on storage failures
rate(godex_sink_errors_total[5m]) > 0.1
```

### Performance Monitoring

```prometheus
# RPC performance
histogram_quantile(0.95, rate(godex_block_fetch_duration_seconds_bucket[5m]))

# Storage throughput
rate(godex_sink_writes_total[5m])
```

### Operational Dashboards

- **Indexing Rate**: Blocks processed per minute
- **Lag Monitoring**: Distance from chain head
- **Error Rates**: RPC failures, storage errors
- **Resource Usage**: Active goroutines, memory patterns

## No-Op Implementation

For environments without metrics collection:

```go
processor := core.NewProcessor(nil, sink)  // nil metrics = no-op
```

All metric calls are no-ops, ensuring zero performance impact.

## Best Practices

### Alerting
- Monitor block lag for indexing delays
- Alert on persistent storage errors
- Track reorg frequency for chain stability

### Dashboards
- Graph block processing rate over time
- Monitor RPC latency percentiles
- Track storage operation success rates

### Debugging
- Use metrics to identify performance bottlenecks
- Correlate errors with system load
- Monitor resource usage during high-throughput periods

## Implementation Notes

- Metrics are chain-scoped for multi-chain deployments
- Histogram buckets are optimized for typical RPC/storage latencies
- Counter values persist across restarts (Prometheus responsibility)
- Gauge values reflect current state and update on changes