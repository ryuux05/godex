# Production operation

## Persistence and replay

The PostgreSQL adapter implements `godex.WindowSink`. A transaction commits the
new internal events, handler effects, and the full processed window cursor.
Empty trailing blocks are included, so restart begins after the processed window
rather than after its last event.

INSERT and COPY both skip existing event IDs. `Handler.Handle` runs only for IDs
inserted by that transaction. An error from the handler or a deferred constraint
at commit rolls back events, handler SQL, and the cursor. Replaying an older
window cannot decrease the cursor; changing the hash at the current height
requires an explicit rollback first.

Each write must contain ordered events for one chain. Heights, timestamps, and
log indices must fit the PostgreSQL column types; event fields must be non-nil
and JSON-serializable. Events at the window end must agree with its cursor hash.
IDs must identify the same event on every replay. IDs are globally unique in the
current schema: deployments indexing networks that can share identical block
and transaction hashes should use separate schemas or a decoder that includes
the chain ID in its event identity. Changing an existing ID format requires a
data migration to preserve replay deduplication.

For a custom sink, implement `WindowSink.StoreWindow` with the same atomic
contract. Existing `Sink` implementations still work, but their `Store` cursor
semantics determine how much of a sparse window is replayed after restart.

## Handler data and reorgs

When a handler writes application tables, implement
`postgres.RollbackHandler`. It receives the first orphaned block **inclusively**
and runs in the same transaction as internal event deletion and cursor rollback.
Application tables need enough chain and block provenance to undo those writes.
Aggregates need reversal or rebuilding from surviving history.

```go
func (h *Handler) Rollback(ctx context.Context, tx pgx.Tx,
    chainID string, fromBlock uint64) error {
    _, err := tx.Exec(ctx, `DELETE FROM app_events
        WHERE chain_id = $1 AND block_num >= $2`, chainID, fromBlock)
    return err
}
```

Handlers that implement only `Handle` remain compatible; the sink cannot undo
their application data automatically. The existing ERC20 and swap example
handlers still need application-specific rollback implementations before their
derived tables can be relied on after a reorg.

Keep effects inside the supplied `pgx.Tx`. HTTP calls, messages, and other external
effects are outside its rollback guarantee. Persist an outbox row in the same
transaction and deliver it using an idempotent consumer when those effects are
needed. A panic releases the transaction, but application code should return
errors for expected failures.

## Startup, shutdown, and resource limits

- Run one active indexer per chain and database namespace. Concurrent duplicate
  inserts are deduplicated, but cursor ownership and competing reorgs are not
  protected by a distributed lease or fencing token.
- `AddChainContext` validates configuration before loading a cursor and bounds
  cursor loading by `RetryConfig.PerRequestTimeout`. `NewSink` bounds schema
  initialization to ten seconds; `NewSinkContext` accepts a caller-owned budget.
- `RangeSize` and `FetcherConcurrency` must be positive. Registration rejects
  duplicate chain IDs, nil dependencies, invalid fetch modes, and invalid retry
  arithmetic. Options, topic/address slices, retry settings, and router rules
  are copied at registration. Finish ABI registration before running.
- `StartBlock` is the initial cursor: indexing starts at `StartBlock + 1`.
  A higher persisted cursor wins. Set it to zero to resume stored progress;
  set it to one less than a desired first indexed height for a new database.
- `MaxInFlightRanges` defaults to twice the worker count. It limits ranges
  awaiting ordered commit, including the arbiter's out-of-order buffer. It is
  a range-count limit, so tune `RangeSize` for your provider's log volume too.
- `PollInterval` defaults to one second when caught up. Polling, worker waits,
  and retry waits observe cancellation. RPC implementations and handlers must
  also honor their supplied contexts. Set the logger before `Run`.
- A processor rejects simultaneous `Run` calls and running without chains.
  Each chain stops on an unrecoverable error; other chains continue. Monitor
  each chain's `IsRunning` and `LastError`, then cancel the shared context to
  shut down the remaining chains before restarting the processor.

## Decoder compatibility

Malformed logs matching a registered event now return an error. The processor
stops before committing that window by default. `SkipDecodeErrors: true`
explicitly opts into discarding those logs and advancing. Logs with no matching
event or route are still ignored.

The standard decoder supports address, bool, bytes32, string, bytes, and signed
and unsigned integer widths from 8 through 256 in steps of eight. Integer widths
and address/boolean padding are checked. Existing `uint8`, `uint16`, `uint32`,
and `uint64` values remain `uint64`; other integers are `*big.Int`. Indexed string
and bytes fields are returned as their 32-byte hashes, because their original
values are not present in the topic.

Arrays, tuples, anonymous events, and other unsupported types are rejected at
ABI registration. Registration failure does not partially publish that ABI.
The legacy `DecodeBatch` methods now return an explicit error because their
signatures lack the ABI/chain context; call `Decode` for each log instead.

## Monitoring and release checks

`Status()` reports start time, per-chain running state, cursor/progress, and
cumulative events processed by this processor instance. Reorgs reset progress
height and rate baselines; event counters include work later rolled back and
are not canonical database row counts. `Health()` allows a caught-up chain to
wait without declaring it stalled and clears recovered errors after success.

Use one metrics collector at the processor layer with the sink's default no-op
metrics, or separate namespaces for processor and sink collectors. Supplying
the same collector to both layers counts shared write/height metrics twice.

CI and tagged releases share the build, vet, race, and PostgreSQL integration
checks. Local instructions are in [testing.md](testing.md). These checks exercise
transaction and recovery behavior; hosted workflows still need to pass on the
actual pull request and release tag.

Further work is tracked in [repository-review.md](repository-review.md), including
distributed ownership, provider consistency during a changing window, and
application-specific example recovery.
