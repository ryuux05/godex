## Processor Architecture

The Processor implements a high-performance concurrent producer-consumer pipeline with comprehensive reorganization handling, designed for scalable EVM-compatible blockchain indexing across multiple chains.

### Core Components

**Producer Layer (Fetchers)**:
- Concurrent workers fetch logs and timestamps using batch RPC requests
- Rate-limited and retry-enabled communication with blockchain nodes
- Individual context timeouts prevent indefinite blocking
- Bounded buffering provides natural backpressure

**Consumer Layer (Arbiter)**:
- Single-threaded coordinator ensures in-order processing for reorg safety
- LRU cache maintains block hash history for efficient reorg detection
- Direct integration with decoder router and sink for atomic event processing
- Context-aware processing with graceful cancellation support

**State Management**:
- Per-chain cursor tracking with persistent storage via sink
- Window-based processing with configurable batch sizes
- Automatic rollback on reorganization detection with ancestor recovery

### Processing Flow

1) **Initialization**:
   - Load cursor from sink or use `StartBlock` configuration
   - Validate chain configuration and decoder/router registration
   - Initialize per-chain state including block hash cache and progress tracking

2) **Head Determination**:
   - Fetch latest block height via `RPC.Head()` with retry logic
   - Calculate safe processing target: `head - ConfirmationDepth`
   - Handle startup reorganization detection if cursor exists

3) **Range Planning**:
   - Divide work into windows of `RangeSize` blocks
   - Distribute ranges to fetcher workers via bounded channel
   - Support for both historical catch-up and live synchronization modes

4) **Concurrent Fetching**:
   - `FetcherConcurrency` workers process ranges in parallel with individual timeouts
   - Each worker fetches logs via `RPC.GetLogs()` or `RPC.GetBlockReceipts()` based on `FetchMode`
   - Optional timestamp fetching via batched `RPC.GetBlocks()` calls for efficiency
   - Results streamed to arbiter with natural backpressure via bounded channel
   - Automatic retry with exponential backoff for transient failures

5) **Ordered Processing**:
   - Arbiter processes fetch results sequentially for reorg safety
   - Verifies block hash continuity using LRU cache during reorg detection
   - Routes logs through decoder router for intelligent event transformation
   - Stores decoded events atomically via sink with transaction rollback support
   - Updates cursor and advances processing window with progress tracking

### Reorganization Handling

The processor implements comprehensive reorg detection and recovery using block hash verification.

**Detection Mechanism**:
- Maintains LRU cache of processed block hashes (`BlockHashCache`)
- Verifies parent hash continuity before processing each window
- Detects divergence when `block.ParentHash != cachedHash[block.Number-1]`

**Recovery Process**:
1. **Ancestor Search**: Binary search backward through cached hashes to find common ancestor
2. **Sink Rollback**: Call `sink.Rollback(chainId, ancestor)` to remove orphaned events
3. **State Reset**: Update cursor to ancestor block and clear future hashes
4. **Resume Processing**: Restart from ancestor + 1 with fresh batch

**Configuration**:
- `ReorgLookbackBlocks`: Maximum blocks to examine during ancestor search (default: 64)
- `ConfirmationDepth`: Blocks to wait before processing to avoid most reorgs

**Performance Characteristics**:
- O(1) hash lookups via LRU cache
- Bounded memory usage with configurable cache size
- Minimal RPC overhead (only fetches headers during reorg detection)

### Error Handling & Resilience

**Context Propagation**: All operations honor context cancellation for graceful shutdown across the entire processing pipeline.

**Error Classification**:
- **Transient errors**: Automatic retry with exponential backoff (RPC timeouts, network issues)
- **Permanent errors**: Immediate failure and context cancellation (invalid configuration, authentication failures)
- **Reorg errors**: Trigger rollback and recovery process with ancestor block detection
- **Non-recoverable errors**: Chain-specific failures that stop individual chain processing

**Concurrency Safety**:
- Workers isolated from each other and main arbiter thread
- Shared state protected by appropriate synchronization primitives
- Clean shutdown waits for all goroutines with proper cancellation propagation
- Individual fetcher contexts prevent resource leaks during cancellation

**Failure Isolation**:
- Individual chain failures do not affect other concurrent chains
- Arbiter processing includes timeout protection to prevent indefinite blocking
- Sink operations use transactions with automatic rollback on failures

### Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `RangeSize` | `int` | Required | Blocks per fetch batch (balances throughput vs memory usage) |
| `FetcherConcurrency` | `int` | Required | Number of concurrent RPC fetch workers |
| `StartBlock` | `uint64` | 0 | Starting block height (0 = resume from stored cursor) |
| `ConfirmationDepth` | `uint64` | Required | Blocks to wait before processing to avoid reorgs |
| `EnableTimestamps` | `bool` | `false` | Include block timestamps in events (additional RPC calls) |
| `Topics` | `[][]string` | Required | Event signature hashes for filtering (supports OR logic) |
| `Addresses` | `[]string` | Optional | Contract addresses to monitor (empty = all addresses) |
| `FetchMode` | `FetchMode` | `FetchModeLogs` | `FetchModeLogs` (efficient) or `FetchModeReceipts` (reliable) |
| `ReorgLookbackBlocks` | `uint64` | 64 | Maximum blocks to examine during reorg ancestor search |
| `UseLogsForHistoricalSync` | `bool` | `true` | Prefer `eth_getLogs` for historical data fetching |
| `RetryConfig` | `*RetryConfig` | Default | Exponential backoff configuration for RPC retries |

### Performance Tuning

**Throughput Optimization**:
- Increase `FetcherConcurrency` to match RPC provider rate limits
- Adjust `RangeSize` based on event density (100-1000 blocks recommended)
- Use `FetchModeReceipts` for targeted contract indexing with lower event volume
- Enable batch timestamp fetching for reduced RPC overhead

**Memory Management**:
- Block hash cache bounded by `ReorgLookbackBlocks` configuration
- Window-based processing naturally limits concurrent memory usage
- Channel buffering provides backpressure without unbounded queue growth
- Individual fetcher timeouts prevent resource leaks during cancellation

**Reorg Resilience**:
- Higher `ConfirmationDepth` reduces reorg frequency but increases processing latency
- Lower `ReorgLookbackBlocks` limits recovery time but reduces reorg detection range
- `UseLogsForHistoricalSync` optimizes initial historical data fetching
- Monitor reorg frequency via metrics to adjust confirmation depth

**RPC Optimization**:
- Configure rate limits to match provider quotas
- Use exponential backoff with jitter to prevent thundering herd problems
- Batch requests automatically utilized for timestamp fetching
- Individual request timeouts prevent indefinite blocking

### Decoder Router Integration

The processor seamlessly integrates with the DecoderRouter for intelligent multi-contract event processing:

**Router Benefits**:
- Single decoder instance can handle multiple contract types
- Automatic routing based on configurable match conditions
- Eliminates the need for multiple processor instances
- Efficient event filtering without redundant decoding attempts

**Integration Pattern**:
```go
// Create router with multiple contract support
router := decoder.NewDecoderRouter().
    Register(decoder.ByTopicCount(3), "ERC20", erc20Decoder).
    Register(decoder.ByTopicCount(4), "ERC721", erc721Decoder).
    Register(decoder.ByAddress("0xUniswap"), "DEX", dexDecoder)

// Register with processor
processor.AddChain(chainInfo, options, router)
```

**Router Processing Flow**:
1. Raw logs fetched from blockchain via RPC
2. Each log evaluated against router match conditions in order
3. First matching route selected for decoding
4. Unmatched logs are silently skipped
5. Decoded events stored atomically via sink

### Multi-Chain Processing

The processor supports concurrent indexing across multiple blockchain networks:

**Chain Isolation**:
- Each chain maintains independent state and cursor tracking
- Individual chain failures do not affect other chains
- Shared resources (decoder, sink) accessed safely via appropriate synchronization

**Configuration Flexibility**:
- Different options per chain (range size, concurrency, confirmation depth)
- Chain-specific retry configurations and rate limits
- Independent progress tracking and metrics collection

**Startup Behavior**:
- Chains start concurrently after initialization
- Cursor resumption from persistent storage
- Graceful handling of chains with different sync states
