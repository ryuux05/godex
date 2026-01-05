# RPC Architecture

## Overview

The RPC client provides resilient communication with EVM-compatible blockchain nodes using JSON-RPC over HTTP. It implements rate limiting, automatic retries, and batch request optimization for high-throughput indexing.

## Core Components

### HTTP Client

**Implementation:**
- JSON-RPC 2.0 protocol over HTTP POST
- Standard `net/http.Client` with 10-second timeout
- Connection reuse via HTTP keep-alive
- Automatic request/response marshaling

**Request Format:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "eth_getLogs",
  "params": [...]
}
```

**Response Format:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": [...],
  "error": null
}
```

**Error Handling:**
- HTTP status codes checked before parsing JSON
- JSON-RPC error objects extracted and wrapped
- Network errors propagated with context

### Rate Limiting

**Token Bucket Algorithm:**
- Implements `golang.org/x/time/rate.Limiter`
- Token bucket with configurable rate and burst capacity
- Automatic token replenishment at specified rate
- Burst allows short bursts above steady rate

**Configuration:**
- `rateLimit`: Requests per second (0 disables limiting)
- `burstLimit`: Maximum tokens available at once
- Rate limiter applies to all RPC calls (single and batch)

**Behavior:**
- Calls wait for available tokens before execution
- Respects context cancellation during wait
- Batch requests consume single token (efficient for bulk operations)

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

The retry configuration controls how transient errors are handled:

```go
retryConfig := &core.RetryConfig{
    MaxAttempts:       3,                // Total attempts (including initial)
    InitialBackoff:    1 * time.Second,  // Starting backoff delay
    MaxBackoff:        30 * time.Second, // Maximum backoff delay
    Multiplier:        2.0,             // Exponential growth factor
    EnableJitter:      true,            // Add randomness to prevent thundering herd
    PerRequestTimeout: 10 * time.Second, // Timeout per individual request
}
```

**Default Configuration:**
```go
defaultConfig := rpc.DefaultRetryConfig()
// MaxAttempts: 3
// InitialBackoff: 1s
// MaxBackoff: 30s
// Multiplier: 2.0
// EnableJitter: true
// PerRequestTimeout: 10s
```

**Retry Behavior:**
- Only retries on retriable errors (network failures, timeouts, rate limits)
- Non-retriable errors (authentication, invalid parameters) fail immediately
- Exponential backoff with jitter prevents synchronized retries
- Context cancellation aborts retry attempts

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
// Single HTTP request fetches multiple blocks
blocks, err := rpc.GetBlocks(ctx, []string{"0x1", "0x2", "0x3"})
// Equivalent to 3 separate GetBlock calls but in 1 HTTP request
// Returns map[string]types.Block with block number as key
```

**Batch Request Format:**
```json
[
  {"jsonrpc": "2.0", "id": 0, "method": "eth_getBlockByNumber", "params": ["0x1", false]},
  {"jsonrpc": "2.0", "id": 1, "method": "eth_getBlockByNumber", "params": ["0x2", false]},
  {"jsonrpc": "2.0", "id": 2, "method": "eth_getBlockByNumber", "params": ["0x3", false]}
]
```

**Benefits:**
- Reduced network overhead (single HTTP request)
- Lower request rate consumption (single token from rate limiter)
- Maintained response correlation via request IDs
- Automatic error handling per request (failed requests skipped in result map)

**Implementation Details:**
- Empty slice returns empty map (no RPC call)
- Failed requests are skipped (not included in result map)
- Request IDs correlate responses to input block numbers
- All requests in batch share same rate limit token

## Error Handling

### Error Types

**Network Errors:**
- Connection failures, timeouts
- DNS resolution failures
- Transport layer errors

**HTTP Errors:**
- 4xx status codes (client errors) - typically non-retriable
- 5xx status codes (server errors) - typically retriable
- Connection timeouts (10s default)

**RPC Errors:**
- JSON-RPC protocol errors (-32700, -32600, -32601, -32602, -32603)
- Provider-specific errors (-32000 to -32099)
- Method not found (-32601)
- Invalid parameters (-32602)

**Rate Limit Errors:**
- Provider quota exceeded
- Rate limit headers (if supported)

### Retry Behavior

**Retriable Errors:**
- Network timeouts and connection failures
- HTTP 429 (Too Many Requests), 5xx server errors
- RPC errors with codes -32000 to -32099 (server errors)
- Context deadline exceeded
- Connection refused, connection reset
- DNS resolution failures
- I/O timeouts

**Non-Retriable Errors:**
- HTTP 4xx client errors (400-428, 430-499)
- JSON-RPC -32700 to -32603 errors:
  - -32700: Parse error
  - -32600: Invalid Request
  - -32601: Method not found
  - -32602: Invalid params
  - -32603: Internal error (may be retriable depending on context)
- Context cancellation
- Authentication failures

**Retry Logic:**
- Automatic retry for transient errors only
- Exponential backoff: `backoff = InitialBackoff * (Multiplier ^ attempt)`
- Jitter adds randomness: `wait = backoff + random(0, backoff/4)`
- Maximum backoff capped at `MaxBackoff`
- Context cancellation aborts retry loop immediately

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
- Configurable timeouts prevent hanging (10s default per request)
- Connection pooling reduces setup costs
- HTTP keep-alive for connection reuse

### Client Configuration

**Default Settings:**
- HTTP timeout: 10 seconds per request
- Rate limiting: Configurable (0 = disabled)
- Burst capacity: Configurable
- Connection reuse: Enabled via HTTP keep-alive

**Customization:**
The `NewHTTPRPC` function creates a client with default settings. For advanced configuration, you can modify the internal `http.Client` or implement a custom `RPC` interface.

**Rate Limiting:**
- Set `rateLimit` to 0 to disable rate limiting
- Burst capacity allows short bursts above steady rate
- Rate limiter applies globally to all RPC calls
- Batch requests consume single token (efficient)

## Usage Examples

### Basic Setup
```go
import "github.com/ryuux05/godex/pkg/core/rpc"

// Create RPC client with rate limiting
rpc := rpc.NewHTTPRPC(
    "https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY",
    20,  // 20 requests per second
    5,   // Burst capacity of 5
)

// Get latest block
head, err := rpc.Head(ctx)
if err != nil {
    return err
}
```

### With Retry Configuration
```go
retryConfig := &rpc.RetryConfig{
    MaxAttempts:       5,
    InitialBackoff:    1 * time.Second,
    MaxBackoff:        60 * time.Second,
    Multiplier:        2.0,
    EnableJitter:      true,
    PerRequestTimeout: 10 * time.Second,
}

err := rpc.RetryWithBackoff(ctx, retryConfig, func() error {
    logs, err := rpc.GetLogs(ctx, filter)
    return err
})
```

### Batch Block Fetching
```go
// Fetch multiple blocks in single request
blockNumbers := []string{"0x1000", "0x1001", "0x1002"}
blocks, err := rpc.GetBlocks(ctx, blockNumbers)
if err != nil {
    return err
}

// Access blocks by number
block1000 := blocks["0x1000"]
```

## Best Practices

### Rate Limit Configuration
- Match provider's documented QPS limits
- Set burst capacity to 20-50% of rate limit
- Monitor rate limit errors and adjust accordingly

### Retry Configuration
- Use 3-5 attempts for most scenarios
- Set `PerRequestTimeout` based on provider latency
- Enable jitter to prevent synchronized retries
- Use exponential backoff for transient failures

### Error Handling
- Always check for context cancellation
- Handle non-retriable errors immediately
- Log retry attempts for debugging
- Monitor retry frequency to identify issues
