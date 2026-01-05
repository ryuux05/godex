# Sink Architecture

## Overview

The Sink component is responsible for persistent storage of decoded blockchain events. It provides a pluggable interface that allows users to implement custom storage backends while maintaining consistency guarantees required for reliable blockchain indexing.

## Design Principles

### 1. Atomicity & Consistency

All sink operations are transactional with strict consistency guarantees. Events are stored atomically with cursor updates, ensuring the system can always resume from a consistent state.

**Core Guarantees**:
- **Atomicity**: All events in a batch succeed or all fail
- **Consistency**: Cursor always reflects stored events
- **Durability**: Events persist across restarts
- **Isolation**: Concurrent operations don't interfere

### 2. Pluggable Architecture

The sink interface enables custom storage backends while maintaining indexer compatibility:

```go
type Sink interface {
    // Store persists events atomically with cursor advancement
    // Events are stored in a transaction, and cursor is updated to the highest block
    Store(ctx context.Context, events []types.Event) error

    // Rollback removes events from specified block onwards
    // Used during blockchain reorganizations to remove orphaned events
    // Updates cursor to (toBlock - 1) atomically
    Rollback(ctx context.Context, chainId string, toBlock uint64, blockHash string) error

    // LoadCursor retrieves last processed block for resumption
    // Returns block number and hash for reorg detection
    // Returns error if cursor not found (first run scenario)
    LoadCursor(ctx context.Context, chainId string) (blockNum uint64, blockHash string, err error)

    // UpdateCursor stores the current block number and hash
    // Used when no events are stored but cursor needs advancement
    // Typically called during empty block processing
    UpdateCursor(ctx context.Context, chainId string, newBlock uint64, blockHash string) error
}
```

**Design Rationale**:
- Minimal interface focuses on essential operations
- Enables diverse backends (PostgreSQL, MongoDB, Kafka, S3)
- Cursor management integrated for consistency

## Architecture Components

### Core Sink Interface

The `Sink` interface represents the contract between the indexer core and storage implementations:

**Store Operation**:
- Accepts a batch of events
- Must be atomic (all succeed or all fail)
- Returns error if any event cannot be stored

**Rollback Operation**:
- Removes events from a block number onwards
- Used during blockchain reorganizations
- Updates cursor to `(toBlock - 1)` with corresponding block hash
- Must be atomic (both event deletion and cursor update)

### Cursor Management

Cursors track indexing progress per chain, enabling reliable restartability:

**Design Principles**:
- **Atomic Updates**: Cursor advances atomically with event storage
- **Consistent State**: Cursor always reflects the highest safely processed block
- **Recovery Support**: System can resume from any stored cursor position

**Cursor Operations**:
- Stored alongside events in the same transaction
- Updated only after successful event persistence
- Used during startup to determine processing resumption point
- Can be updated independently via `UpdateCursor` when no events are stored

**UpdateCursor Method:**
- Advances cursor without storing events
- Used when processing empty blocks (no events in range)
- Ensures cursor always reflects processing progress
- Atomic operation (cursor update only)

**Usage Example:**
```go
// Update cursor when no events found in block range
err := sink.UpdateCursor(ctx, "1", 18000000, "0xabc123...")
if err != nil {
    return fmt.Errorf("failed to update cursor: %w", err)
}
```

**When to Use:**
- Processing blocks with no matching events
- Maintaining cursor consistency during empty ranges
- Ensuring resumption from correct block after restart

### Cursor Management

Cursors track processing progress per chain, enabling restartability. The cursor pattern separates progress tracking from event storage:

**Design Decision**: Cursors are updated within the same transaction as event storage, ensuring they always reflect accurate progress.

**Why Separate Interface**: Cursors may be stored differently than events (e.g., Redis for cursors, PostgreSQL for events), so they're separated into a `CursorStore` interface.

## PostgreSQL Adapter Architecture

### PostgreSQL Implementation

The PostgreSQL adapter provides production-ready event storage with optimized performance:

**Storage Strategy**:
- **Adaptive Mode**: Automatically chooses between INSERT and COPY based on batch size
- **COPY Protocol**: High-throughput bulk loading for large batches
- **INSERT Mode**: Standard SQL insertion for smaller batches

**Schema Design**:
- **chronicle_events**: Stores all indexed events with idempotent keys
- **chronicle_cursors**: Tracks per-chain processing progress
- **Optimized Indexes**: Composite indexes for common query patterns

**Performance Characteristics**:
- COPY mode: 10-50x faster for bulk operations
- Transactional consistency with cursor updates
- Connection pooling via `pgxpool`
- Configurable batch thresholds

**Initialization:**
```go
import "github.com/ryuux05/godex/adapters/sink/postgres"

// Create connection pool
pool, err := pgxpool.New(ctx, "postgres://user:pass@host:5432/db")
if err != nil {
    return err
}

// Create handler for custom schema logic
handler := &MyEventHandler{}

// Create sink with configuration
sink, err := postgres.NewSink(postgres.SinkConfig{
    Pool:          pool,
    Handler:       handler,
    CopyThreshold: 32,  // Switch to COPY for batches >= 32 events
    Metrics:       metrics, // Optional metrics (defaults to Noop)
})
```

**Configuration Options:**
- `Pool`: Required PostgreSQL connection pool (`*pgxpool.Pool`)
- `Handler`: Required handler for custom business logic
- `CopyThreshold`: Optional threshold for COPY mode (default: 32)
- `Metrics`: Optional metrics instance (defaults to `metrics.Noop{}`)

### Handler Interface

The Handler interface allows custom business logic to run within the same transaction as event storage:

```go
type Handler interface {
    Handle(ctx context.Context, tx pgx.Tx, event types.Event) error
}
```

**Handler Execution:**
- Runs in the same transaction as event storage
- Executed for each event in the batch sequentially
- Any error rolls back the entire transaction (events + handler logic)
- Receives `pgx.Tx` for custom SQL operations

**Example Handler:**
```go
type ERC20Handler struct{}

func (h *ERC20Handler) Handle(ctx context.Context, tx pgx.Tx, event types.Event) error {
    if event.EventType == "Transfer" {
        from := event.Fields["from"].(string)
        to := event.Fields["to"].(string)
        value := event.Fields["value"].(*big.Int)
        
        // Custom logic in same transaction
        _, err := tx.Exec(ctx, `
            INSERT INTO token_transfers (chain_id, from_addr, to_addr, value)
            VALUES ($1, $2, $3, $4)
        `, event.ChainID, from, to, value.String())
        return err
    }
    return nil
}
```

### Internal Schema Design

The adapter maintains internal tables for its own operations:

**chronicle_events Table**:
- Stores all indexed events
- Primary key: `event_id` (idempotent key)
- Indexes optimized for common query patterns:
  - `(chain_id, block_num)` - Range queries
  - `(chain_id, kind, block_num)` - Event type filtering
  - `(chain_id, address, block_num)` - Contract filtering

**chronicle_cursors Table**:
- Tracks processing progress per chain
- Updated atomically with event storage
- Enables restartability after downtime

**Design Principles**:
- **Idempotency**: `event_id` prevents duplicate storage
- **Query Optimization**: Indexes match common access patterns
- **Separation**: Internal schema separate from user schema

## Architectural Patterns

### 1. Strategy Pattern (Storage Modes)

The adapter uses the Strategy pattern to switch between INSERT and COPY modes:

```go
useCopy := len(events) >= s.copyThreshold

if useCopy {
    err = s.copyInternalEvents(ctx, tx, events)
} else {
    err = s.insertInternalEvents(ctx, tx, events)
}
```

**Benefits**:
- Encapsulates storage logic
- Easy to add new strategies (e.g., batch INSERT)
- Transparent to callers

### 2. Template Method Pattern (Transaction Flow)

The transaction flow follows the Template Method pattern:

1. Begin transaction (template)
2. Store events (hook)
3. Execute handlers (hook)
4. Update cursor (hook)
5. Commit/rollback (template)

**Benefits**:
- Consistent transaction management
- Error handling centralized
- Easy to extend with additional hooks

### 3. Dependency Injection (Handler)

Handlers are injected via configuration, following Dependency Injection:

```go
type SinkConfig struct {
    Handler Handler  // Injected dependency
}
```

**Benefits**:
- Testability (mock handlers)
- Flexibility (different handlers per sink instance)
- Separation of concerns

## Performance Architecture

### Batch Processing

Events are processed in batches rather than individually:

**Why Batches**:
- Reduces transaction overhead
- Enables bulk operations (COPY)
- Better throughput

**Batch Size Considerations**:
- Too small: High transaction overhead
- Too large: Long-running transactions, memory usage
- Optimal: Balance between overhead and latency

### Connection Pooling

The adapter uses `pgxpool` for connection management:

**Architecture Benefits**:
- Connection reuse reduces overhead
- Automatic connection lifecycle management
- Configurable pool size based on workload

**Pool Configuration**:
- `MaxConns`: Maximum concurrent connections
- `MinConns`: Minimum idle connections
- Connection health checks and recovery

### Indexing Strategy

Indexes are designed for common query patterns:

**Primary Indexes**:
- Event lookup by `event_id` (primary key)
- Range queries by `(chain_id, block_num)`
- Filtering by event type `(chain_id, kind, block_num)`
- Contract-specific queries `(chain_id, address, block_num)`

**Design Trade-offs**:
- More indexes = faster queries, slower writes
- Balance based on read/write ratio
- User can add custom indexes via migrations

## Error Handling Architecture

### Error Propagation

Errors propagate immediately, triggering rollback:

**Error Types**:
1. **Storage Errors**: Database connection, query execution failures
2. **Handler Errors**: Business logic validation failures
3. **Constraint Errors**: Unique constraint violations (handled gracefully)

**Error Handling Strategy**:
- Immediate propagation (no retry at sink level)
- Transaction rollback on any error
- Clear error messages for debugging

### Atomicity Guarantees

All operations are atomic:

**Store Operation**:
- Events stored + handlers executed + cursor updated = atomic
- Any failure rolls back entire operation

**Rollback Operation**:
- Event deletion + cursor update = atomic
- Any failure rolls back entire operation

**Why Critical**: Partial operations would leave the system in an inconsistent state, making it impossible to reliably resume processing.

## Reorg Handling Architecture

### Rollback Mechanism

The `Rollback` method handles blockchain reorganizations:

**Operation Flow**:
1. Begin transaction
2. Delete events from `toBlock` onwards
3. Update cursor to `(toBlock - 1)` with corresponding `blockHash`
4. Commit

**Method Signature:**
```go
Rollback(ctx context.Context, chainId string, toBlock uint64, blockHash string) error
```

**Design Considerations**:
- **Efficiency**: Single DELETE query for all events
- **Atomicity**: Both deletion and cursor update in one transaction
- **Safety**: Handles edge case of rolling back to block 0

### Cursor Consistency

Cursors must always reflect accurate progress:

**Consistency Rules**:
- Cursor updated atomically with event storage
- Cursor updated atomically with rollback
- Cursor never ahead of stored events

**Why Critical**: Inconsistent cursors would cause duplicate processing or missed events.

## Extension Points

### Custom Sink Implementation

Users can implement custom sinks:

**Implementation Requirements**:
- Satisfy `Sink` interface
- Ensure atomicity
- Handle errors appropriately

**Use Cases**:
- Different storage backends (MongoDB, Kafka, S3)
- Custom data models
- Integration with existing systems

### Migration System

The PostgreSQL adapter provides migration utilities:

**Architecture**:
- User-defined migrations via `Migrate` and `MigrateWithFile`
- Each migration runs in a transaction
- Idempotent migrations (use `IF NOT EXISTS`)

**Design Rationale**:
- Separates internal schema from user schema
- Version control for schema changes
- Safe to run multiple times

## Integration Architecture

### Processor Integration

The sink integrates with the Processor:

**Integration Points**:
- Processor can optionally use a sink for automatic storage
- Sink receives events after decoding
- Sink handles storage, processor handles fetching/decoding

**Separation of Concerns**:
- **Processor**: Fetching, decoding, reorg detection
- **Sink**: Storage, persistence, business logic

### Decoder Integration

Sinks receive decoded events:

**Event Flow**:
1. Processor fetches raw logs
2. Decoder transforms logs to events
3. Sink stores events

**Why Decoded**: Sinks operate on structured events, not raw logs. This separation allows sinks to be chain-agnostic.

## Design Trade-offs

### Transaction Size vs. Throughput

**Trade-off**: Larger transactions (bigger batches) = higher throughput but longer lock times

**Decision**: Use configurable batch sizes with COPY mode for large batches

### Handler Performance vs. Atomicity

**Trade-off**: Sequential handler execution ensures atomicity but limits parallelism

**Decision**: Prioritize atomicity over parallelism. Handlers should be lightweight.

### Storage Efficiency vs. Query Performance

**Trade-off**: More indexes = faster queries but slower writes

**Decision**: Provide essential indexes, allow users to add custom indexes via migrations

### Simplicity vs. Features

**Trade-off**: Minimal interface vs. feature-rich implementation

**Decision**: Keep interface minimal, provide rich PostgreSQL implementation. Users can extend via custom implementations.

## Future Architectural Considerations

### Distributed Sink Support

For distributed indexing, sinks could support:
- Sharding by chain or block range
- Distributed transaction coordination
- Replication strategies

### Event Streaming

Sinks could support streaming patterns:
- Event sourcing
- Change data capture
- Real-time event streams

### Multi-Backend Support

Single sink could write to multiple backends:
- Primary storage (PostgreSQL)
- Secondary storage (S3 for archival)
- Cache layer (Redis for hot data)

## Summary

The Sink architecture prioritizes:
1. **Atomicity**: All operations are transactional
2. **Pluggability**: Interface-based design allows custom implementations
3. **Performance**: Adaptive strategies optimize for different workloads
4. **Consistency**: Cursors and events always consistent
5. **Extensibility**: Handler pattern and migration system enable customization

This architecture ensures reliable, performant event storage while maintaining flexibility for diverse use cases.

