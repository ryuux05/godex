# RPC Architecture

## Overview

The RPC client provides resilient communication with EVM-compatible blockchain nodes using JSON-RPC over HTTP. It implements rate limiting, automatic retries, and batch request optimization for high-throughput indexing.

## Core Components

### HTTP Client
- JSON-RPC 2.0 protocol implementation
- Configurable timeout and connection pooling
- HTTP status code and JSON-RPC error handling

### Rate Limiting
- Token bucket algorithm for steady throughput
- Configurable requests per second and burst capacity
- Prevents provider quota exhaustion

### Retry Logic
- Exponential backoff with jitter
- Configurable retry attempts and delays
- Automatic retry for transient network errors

### Batch Optimization
- Automatic batching for `GetBlocks` requests
- Reduces RPC call count for timestamp fetching
- Maintains request ordering and correlation

## Configuration

### Client Setup
```go
rpc := core.NewHTTPRPC(
    "https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY",  // RPC endpoint
    20,  // Requests per second
    5,   // Burst capacity
)
```

### Retry Configuration
```go
retryConfig := &core.RetryConfig{
    MaxAttempts:    3,
    InitialBackoff: time.Second,
    MaxBackoff:     30 * time.Second,
    Multiplier:     2.0,
    EnableJitter:   true,
}
```

## Interface Methods

| Method | Purpose | Batch Support |
|--------|---------|---------------|
| `Head()` | Latest block number | No |
| `GetBlock(number)` | Single block header | No |
| `GetBlocks(numbers[])` | Multiple block headers | **Yes** |
| `GetLogs(filter)` | Event logs in range | No |
| `GetBlockReceipts(number)` | Transaction receipts | No |

## Batch Request Optimization

The `GetBlocks` method automatically uses JSON-RPC batching for efficiency:

```go
// Single call fetches multiple blocks
blocks, err := rpc.GetBlocks(ctx, []string{"0x1", "0x2", "0x3"})
// Equivalent to 3 separate GetBlock calls but in 1 HTTP request
```

**Benefits:**
- Reduced network overhead
- Lower request rate consumption
- Maintained response correlation

## Error Handling

### Error Types
- **Network Errors**: Connection failures, timeouts
- **HTTP Errors**: 4xx/5xx status codes
- **RPC Errors**: JSON-RPC protocol errors (-32700, -32600, etc.)
- **Rate Limit Errors**: Provider quota exceeded

### Retry Behavior
- Automatic retry for transient errors
- Exponential backoff prevents thundering herd
- Configurable retry limits and delays

## Performance Characteristics

### Throughput
- Rate limiting ensures sustainable request patterns
- Batch requests maximize efficiency
- Connection reuse via HTTP keep-alive

### Reliability
- Retry logic handles temporary failures
- Graceful degradation on persistent issues
- Context cancellation for clean shutdown

### Resource Usage
- Minimal memory overhead
- Configurable timeouts prevent hanging
- Connection pooling reduces setup costs
