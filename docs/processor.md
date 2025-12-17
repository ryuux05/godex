## Processor Architecture

The Processor implements a concurrent producer-consumer pattern with built-in reorganization handling, designed for high-throughput EVM-compatible blockchain indexing.

### Core Components

**Producer Layer (Fetchers)**:
- Concurrent workers fetch logs and timestamps using batch RPC requests
- Rate-limited and retry-enabled communication with blockchain nodes
- Bounded buffering prevents memory exhaustion

**Consumer Layer (Arbiter)**:
- Single-threaded coordinator ensures ordered processing
- LRU cache maintains block hash history for reorg detection
- Direct integration with decoder and sink for atomic event processing

**State Management**:
- Per-chain cursor tracking with persistent storage
- Window-based processing with configurable batch sizes
- Automatic rollback on reorganization detection

### Processing Flow

1) **Initialization**:
   - Load cursor from sink or use `StartBlock`
   - Validate chain configuration and decoder registration

2) **Head Determination**:
   - Fetch latest block height via `RPC.Head()`
   - Calculate safe processing target: `head - ConfirmationDepth`

3) **Range Planning**:
   - Divide work into windows of `RangeSize` blocks
   - Distribute ranges to fetcher workers via bounded channel

4) **Concurrent Fetching**:
   - `FetcherConcurrency` workers process ranges in parallel
   - Each worker fetches logs via `RPC.GetLogs()` with topic filters
   - Optional timestamp fetching via batched `RPC.GetBlocks()` calls
   - Results sent to arbiter with natural backpressure via bounded channel

5) **Ordered Processing**:
   - Arbiter processes windows sequentially for reorg safety
   - Verifies block hash continuity using LRU cache
   - Decodes logs to events and stores atomically via sink
   - Updates cursor and advances processing window

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

**Context Propagation**: All operations honor context cancellation for graceful shutdown.

**Error Classification**:
- **Transient errors**: Automatic retry with exponential backoff
- **Permanent errors**: Immediate failure and context cancellation
- **Reorg errors**: Trigger rollback and recovery process

**Concurrency Safety**:
- Workers isolated from each other and main arbiter
- Shared state protected by appropriate synchronization
- Clean shutdown waits for all goroutines

### Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `RangeSize` | `int` | - | Blocks per fetch batch (balances throughput vs rollback size) |
| `FetcherConcurrency` | `int` | - | Concurrent RPC fetch workers |
| `StartBlock` | `uint64` | 0 | Starting block (0 = resume from cursor) |
| `ConfirmationDepth` | `uint64` | - | Blocks to wait before processing |
| `EnableTimestamps` | `bool` | `false` | Fetch block timestamps (additional RPC cost) |
| `Topics` | `[]string` | - | Event signatures for filtering |
| `Addresses` | `[]types.Address` | - | Contract addresses to monitor |
| `FetchMode` | `FetchMode` | `"logs"` | `"logs"` or `"receipts"` |
| `ReorgLookbackBlocks` | `uint64` | 64 | Max blocks for reorg ancestor search |
| `UseLogsForHistoricalSync` | `bool` | `true` | Prefer `eth_getLogs` for historical data |

### Performance Tuning

**Throughput Optimization**:
- Increase `FetcherConcurrency` to match RPC rate limits
- Adjust `RangeSize` for optimal batch efficiency
- Use `FetchModeReceipts` for contract-specific indexing

**Memory Management**:
- `BlockHashCache` bounded by `ReorgLookbackBlocks`
- Window processing naturally limits in-flight memory
- Channel buffering provides backpressure without unbounded growth

**Reorg Resilience**:
- Higher `ConfirmationDepth` reduces reorg frequency
- Lower `ReorgLookbackBlocks` limits recovery time
- `UseLogsForHistoricalSync` optimizes historical data fetching
