# Architecture

godex implements a high-performance producer-consumer pipeline with concurrent fetching, ordered processing, and fault-tolerant storage designed for production blockchain indexing.

## System Architecture

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

## Core Components

| Component | Responsibility | Implementation |
|-----------|----------------|----------------|
| **Processor** | Orchestrates multi-chain indexing lifecycle with error isolation | Concurrent per-chain processing with shared resources |
| **Fetchers** | Concurrent RPC workers fetching logs and timestamps | Rate-limited batch requests with individual timeouts and retry logic |
| **Arbiter** | Maintains block order and coordinates processing pipeline | LRU cache for reorg detection, bounded buffering with context cancellation |
| **Decoder Router** | Intelligent event routing to appropriate decoders | Match-based routing with support for multiple contract types |
| **Sink** | Persistent storage with atomic rollback support | Transactional writes with reorg recovery and cursor management |

## Processing Flow

1. **Initialization**: Load cursor from sink or use `StartBlock` configuration
2. **Head Determination**: Fetch latest block height via `RPC.Head()` with retry logic
3. **Range Planning**: Divide work into windows of `RangeSize` blocks
4. **Concurrent Fetching**: `FetcherConcurrency` workers process ranges in parallel
5. **Ordered Processing**: Arbiter processes fetch results sequentially for reorg safety
6. **Event Decoding**: Router routes logs to appropriate decoders
7. **Storage**: Decoded events stored atomically via sink with transaction rollback support

## Core Interfaces

### Sink Interface

Persistent storage abstraction with transactional rollback support.

```go
type Sink interface {
    // Store persists a batch of events atomically
    Store(ctx context.Context, events []types.Event) error

    // Rollback removes events from specified block onwards during reorgs
    Rollback(ctx context.Context, chainId string, toBlock uint64, blockHash string) error

    // LoadCursor retrieves the last processed block for resumption
    LoadCursor(ctx context.Context, chainId string) (blockNum uint64, blockHash string, err error)

    // UpdateCursor stores the current block number and hash
    UpdateCursor(ctx context.Context, chainId string, newBlock uint64, blockHash string) error
}
```

### Decoder Interface

Event transformation with pluggable ABI support.

```go
type Decoder interface {
    // Decode transforms a single log into a structured event
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

### Metrics Interface

Observability and monitoring interface.

```go
type Metrics interface {
    // Block processing metrics
    IncBlocksProcessed(chainId string, n uint64)
    ObservedBlockLag(chainId string, lag uint64)
    ObservedBlockFetchDuration(chainId string, d time.Duration, success bool)

    // Storage metrics
    IncSinkWrites(chainId string, n uint64)
    IncSinkErrors(chainId string)
    ObservedSinkWriteDuration(chainId string, d time.Duration, success bool)

    // Indexer state
    SetIndexedHeight(chainId string, height uint64)
    SetProcessorConcurrency(chainId string, n uint64)
    IncReorgs(chainId string)
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
2. **Sink Rollback**: Call `sink.Rollback(chainId, ancestor, blockHash)` to remove orphaned events atomically
3. **State Reset**: Update cursor to ancestor block and clear future hash cache entries
4. **Resume Processing**: Restart from ancestor + 1 with fresh batch processing

**Performance Characteristics:**
- O(1) hash lookups via LRU cache
- Bounded memory usage with configurable cache size
- Minimal RPC overhead (only fetches headers during reorg detection)

**Configuration Options:**
- `ReorgLookbackBlocks`: Maximum blocks to examine (default: 64, balances detection range vs memory)
- `ConfirmationDepth`: Blocks to wait before processing (higher = fewer reorgs but increased latency)

## Concurrency Model

### Producer-Consumer Pattern

**Producer Layer (Fetchers):**
- Concurrent workers fetch logs and timestamps using batch RPC requests
- Rate-limited and retry-enabled communication with blockchain nodes
- Individual context timeouts prevent indefinite blocking
- Bounded buffering provides natural backpressure

**Consumer Layer (Arbiter):**
- Single-threaded coordinator ensures in-order processing for reorg safety
- LRU cache maintains block hash history for efficient reorg detection
- Direct integration with decoder router and sink for atomic event processing
- Context-aware processing with graceful cancellation support

### Multi-Chain Processing

**Chain Isolation:**
- Each chain maintains independent state and cursor tracking
- Individual chain failures do not affect other chains
- Shared resources (decoder, sink) accessed safely via appropriate synchronization

**Configuration Flexibility:**
- Different options per chain (range size, concurrency, confirmation depth)
- Chain-specific retry configurations and rate limits
- Independent progress tracking and metrics collection

## Error Handling Architecture

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

## Performance Architecture

### Batch Processing

Events are processed in batches rather than individually:
- Reduces transaction overhead
- Enables bulk operations (COPY mode in PostgreSQL)
- Better throughput

### Connection Pooling

The PostgreSQL adapter uses `pgxpool` for connection management:
- Connection reuse reduces overhead
- Automatic connection lifecycle management
- Configurable pool size based on workload

### Memory Management

- **Bounded Caching**: LRU block hash cache prevents unbounded memory growth
- **Window Processing**: Natural backpressure limits concurrent memory usage
- **Channel Buffering**: Sized channels provide backpressure without excessive queuing
- **Resource Cleanup**: Context cancellation ensures prompt resource release

## Design Principles

1. **Atomicity**: All operations are transactional
2. **Pluggability**: Interface-based design allows custom implementations
3. **Performance**: Adaptive strategies optimize for different workloads
4. **Consistency**: Cursors and events always consistent
5. **Extensibility**: Handler pattern and migration system enable customization

For detailed documentation on specific components, see:
- [Processor Architecture](processor.md)
- [Decoder Architecture](decoder.md)
- [RPC Architecture](rpc.md)
- [Sink Architecture](sink.md)

