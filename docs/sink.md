# Sink Architecture

## Overview

The Sink component is responsible for persistent storage of decoded blockchain events. It provides a pluggable interface that allows users to implement custom storage backends while maintaining consistency guarantees required for reliable blockchain indexing.

## Design Principles

### 1. Atomicity First

All sink operations are transactional. The core principle is **all-or-nothing**: either all events in a batch are stored successfully, or none are stored at all. This ensures data consistency even when handlers fail or storage operations encounter errors.

**Rationale**: Blockchain indexing requires strict consistency. Partial batches would lead to inconsistent state, making it impossible to reliably track which events have been processed.

### 2. Handler Integration Pattern

Handlers execute within the same transaction as event storage, creating a unified atomic operation. This design ensures that:
- Business logic failures trigger rollback of event storage
- Event storage failures prevent handler side effects
- Both operations succeed or fail together

**Architectural Benefit**: Eliminates the need for complex two-phase commit protocols or eventual consistency mechanisms. The database transaction provides the atomicity guarantee.

### 3. Pluggable Interface

The sink is defined by a minimal interface, allowing users to implement custom storage backends (PostgreSQL, MongoDB, Kafka, file systems, etc.) without modifying core indexer logic.

**Interface Design**:
```go
type Sink interface {
    Store(ctx context.Context, events []types.Event) error
    Rollback(ctx context.Context, chainID string, toBlock uint64) error
}
```

**Why Minimal**: The interface focuses on essential operations only. Additional features (cursors, migrations) are implementation-specific and don't belong in the core interface.

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
- Must be atomic

### Handler Pattern

Handlers provide a mechanism for executing user-defined business logic within the storage transaction:

**Design Rationale**:
1. **Transaction Sharing**: Handlers receive the same transaction as event storage, ensuring atomicity
2. **Sequential Execution**: Handlers run sequentially to maintain deterministic order
3. **Early Failure**: Handler failures prevent event storage, enabling validation before persistence

**Handler Interface**:
```go
type Handler interface {
    Handle(ctx context.Context, tx pgx.Tx, ev types.Event) error
}
```

**Architectural Constraints**:
- Handlers must use the provided transaction for database operations
- Handler errors trigger transaction rollback
- Handlers cannot commit or rollback the transaction (managed by sink)

### Cursor Management

Cursors track processing progress per chain, enabling restartability. The cursor pattern separates progress tracking from event storage:

**Design Decision**: Cursors are updated within the same transaction as event storage, ensuring they always reflect accurate progress.

**Why Separate Interface**: Cursors may be stored differently than events (e.g., Redis for cursors, PostgreSQL for events), so they're separated into a `CursorStore` interface.

## PostgreSQL Adapter Architecture

### Dual-Mode Storage Strategy

The PostgreSQL adapter implements two storage strategies based on batch size:

**INSERT Mode** (Small Batches):
- Uses individual `INSERT` statements
- Suitable for frequent, small batches
- Lower overhead for small operations
- Threshold: < `CopyThreshold` events

**COPY Mode** (Large Batches):
- Uses PostgreSQL's `COPY FROM` protocol
- Bypasses query planner and executor
- Direct to storage engine
- Threshold: >= `CopyThreshold` events

**Architectural Rationale**:
- **Performance**: COPY protocol is 10-50x faster for bulk operations
- **Adaptive**: Automatically selects optimal strategy based on batch size
- **Transparent**: Switching is internal to the adapter, no API changes

### Transaction Management

All operations occur within database transactions:

**Transaction Flow**:
1. Begin transaction
2. Store events (INSERT or COPY)
3. Execute handlers sequentially
4. Update cursor
5. Commit (or rollback on error)

**Error Handling Strategy**:
- Deferred rollback ensures cleanup on any error
- Errors propagate immediately, preventing partial commits
- Transaction boundaries are explicit and controlled

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
3. Update cursor to `toBlock - 1`
4. Commit

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

