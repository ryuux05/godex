# Repository review and test improvements

The review traversed `pkg/core`, the public `pkg/godex` API, both adapters,
examples, documentation, and build/CI configuration. The existing graph helped
locate processor, cursor, arbiter, and sink relationships. Current source and
executed tests were used to verify behavior because the graph predates the
working tree changes.

## Added coverage

This change adds 58 test functions and three fuzz targets, including table-driven
cases. Tests concentrate on failure handling, boundaries, persistence, and
concurrent execution. Existing working tree edits were retained.

| Package | Baseline | After review |
| --- | ---: | ---: |
| Processor | 68.3% | 90.6% |
| Decoder | 53.7% | 96.9% |
| RPC | 83.3% | 92.3% |
| PostgreSQL sink | 0.0% | 88.0% |
| Prometheus metrics | 50.0% | 100.0% |
| Error classification | 0.0% | 100.0% |
| Utilities | 0.0% | 100.0% |
| Public SDK | 0.0% | 100.0% |

The baseline PostgreSQL tests skipped because no database was configured. After
review, they were executed against an isolated PostgreSQL 16 database. SDK and
adapter statement coverage rose from 54.8% to 92.1%; the repository
total includes untested example applications. Coverage is package-local, so
calls from another package's tests do not inflate these package figures.

## Defects addressed

- Fetcher errors were discarded. Workers now cancel their peers and deliver
  their final error to the batch caller.
- Batch completion could win a select while an arbiter error was pending. Both
  pipeline stages are joined and their errors checked before success is returned.
- Workers blocked waiting for jobs did not observe cancellation. That wait now
  selects on the context.
- Receipt topic matching accepted any matching position. Every position now
  matches, with alternatives within a position and wildcard positions supported.
- Reorg rollback deleted the canonical ancestor and persisted a cursor one block
  behind the in-memory cursor. The processor now passes `ancestor + 1` to the
  inclusive sink rollback operation.
- Startup reorg recovery advanced the in-memory cursor before rollback succeeded.
  Cursor publication now follows successful persistence.
- Live reorg searches below the historical range size jumped straight to zero.
  Live searches now step back one block.
- Hash-cache pruning assumed LRU order matched block-height order. It now removes
  every orphaned height regardless of access order.
- Concurrent status reads could race with cursor commits. Cursor writes now use
  the processor mutex also held by status readers.
- Lag calculation applied confirmation depth twice. It now uses the confirmed
  target returned by planning.
- Malformed ABI topic/data slices and dynamic pointers could panic. Prefix and
  bounds checks now reject those logs; dynamic bounds are checked before integer
  multiplication. Invalid address hex and boolean padding are rejected too.
- Negative signed ABI integers were decoded as positive values. Supported signed
  types now decode signed values and enforce their range.
- Retry jitter could panic for zero or tiny backoffs. Jitter requires a positive
  random bound, and canceled contexts are checked before invoking a callback.
- Wrapped cancellation was reported as a fatal chain error. It is now recognized
  as shutdown, and progress logging is canceled when its chain exits.
- RPC batch errors lost their typed error codes, preventing transient timestamp
  requests from retrying. Wrapped RPC errors now retain their code and data.
- PostgreSQL tests silently skipped a configured unavailable database, leaked
  pools, and shared tables. Tests now own isolated schemas and fail on a broken
  configured connection. Two stale constructor error assertions were corrected.
- CI had duplicate YAML keys, a lint job outside `jobs`, and no database service.
  It now builds, vets, runs race tests with PostgreSQL, and saves coverage.

## Production hardening pass

The production pass adds 29 test functions with table-driven cases and addresses
persistence and execution contracts beyond statement coverage. At that stage SDK and
adapter statement coverage was 92.3%:

- Conflict-safe COPY staging now matches INSERT replay behavior. Only new IDs
  invoke database handlers, including concurrent replay and threshold transitions.
- The optional `WindowSink` operation commits events, handler data, and full-window
  cursor atomically. Old replay cannot regress the cursor; a conflicting current
  hash fails without committing. PostgreSQL implements this capability.
- Optional transactional handler rollback supports application-owned reorg data.
  Begin, handler, deferred commit, rollback-hook, and panic cleanup paths are tested.
- Configuration validation runs before persistence access; registration snapshots
  mutable settings and routes, rejects duplicates, and offers bounded cursor loading.
- Outstanding ranges are bounded through ordered-commit acknowledgments. Idle
  polling is configurable and cancellable, lifecycle calls are guarded, and chain
  lookup/registration is safe during concurrent reads.
- Status includes start time, running flags, and aggregate event counts. Rollback
  resets progress, successful recovery clears errors, and caught-up idle chains
  stay healthy.
- Malformed matching ABI fields stop processing by default. All integer widths
  enforce their declared bounds; indexed dynamic values retain their hashes.
  Unsupported ABIs and context-free batch decoding fail explicitly.
- RPC null/missing results and missing/mismatched IDs are rejected. Zero burst
  with positive rate limiting receives a usable default, and retry arithmetic is
  validated and capped before conversion.
- Repeated schema initialization retains existing index names. Library build
  targets and public API examples are corrected; tagged releases reuse CI's
  PostgreSQL and race gates.
- A real PostgreSQL integration test covers sparse-window persistence, live reorg,
  projection rollback, replacement branch indexing, cancellation, and restart.

## Example recovery follow-up

Ten database test functions now cover both application handlers. Both implement
transactional rollback with chain/event/block provenance. Tests cover inclusive
boundaries, replacement branch replay under INSERT and COPY, preservation of
other chains, failed rollback and failed writes, and rebuilding legacy schemas
without resetting cursors. The ERC20 rebuild fixture spans multiple pages and
preserves 256-bit integer precision; Uniswap fixtures restore bytes32 pool IDs.

Swap identity now distinguishes separate logs in one transaction, and pool
identity includes the chain. Cross-chain connection writes no longer reuse a
busy database connection or ignore failures, and early timestamps no longer
underflow the five-minute matching window. Both examples use processor metrics
without attaching the same collector to the sink.

Legacy application schemas require an explicit `REBUILD_PROJECTIONS=1` startup.
The rebuild is transactional, recreates derived tables from stored events, and
preserves indexing progress. Operational details and limits are in
[production.md](production.md#upgrading-existing-example-databases).

## Remaining priorities

These are concrete boundaries of this pass, with deployment guidance in
[production.md](production.md). The supported design has one indexer per chain;
distributed ownership is outside that design.

| Priority | Finding | Next work |
| --- | --- | --- |
| High | Log fetch and header reads can observe different canonical views when a provider reorganizes during a window. | First test reorgs between the full-window log response and the later ending-header request; receipts and split windows involve additional calls. Use the results to decide whether extra consistency checks are needed. Confirmation depth reduces exposure. |
| Medium | Global event IDs can collide across networks sharing the same block/transaction hashes; existing IDs lack chain namespace. | Design a migration for chain-scoped identity and existing handler references. Use separate schemas or chain-namespaced custom IDs for affected deployments. |
| Medium | Arrays, tuples, anonymous events, and fixed byte sizes other than bytes32 are unsupported and now rejected explicitly. | Add ABI conformance fixtures and implement supported extensions with a defined compatibility contract. |
| Medium | PostgreSQL's replay-safe COPY now stages rows and merges them, adding per-batch work. | Benchmark INSERT/COPY crossover with realistic payload sizes and replay proportions before tuning the threshold. |
| Medium | Reorg search has a bounded in-memory history and a conservative fallback; headers are not persisted for exact recovery across restarts. | Test deep reorg/fallback budgets and consider durable header history for exact ancestor discovery. |
| Low | Metrics can count the same write twice when one collector is supplied to both processor and sink. | Define layer-specific metric ownership; currently use processor metrics and the sink's default no-op, or separate namespaces. |

## Validation

The expanded suite was run with race detection and a real isolated PostgreSQL
database. Build and `go vet` checks were run. All three fuzz targets ran for five
seconds each. Concurrent failure, cancellation, and status tests were repeated
30 times under the race detector. CI YAML was parsed with duplicate-key detection
and checked for its service, DSN, and race-test step; the hosted GitHub workflow
itself has not been executed during this local review.

Commands and database setup are documented in [testing.md](testing.md).


For the production pass, the full race suite passed with PostgreSQL configured;
`make build vet` passed, and the README quick-start compiled independently.
CI/release YAML passed duplicate-key and gate-configuration checks. The log
decoder fuzz target ran for five seconds with 781,768 executions and no failure.
Hosted workflows have not been run locally. The disposable database was removed
after validation.

For the example recovery follow-up, the full race suite passed against
PostgreSQL 16. Package statement coverage is 28.9% for ERC20 and 53.2% for
Uniswap; application entry points remain untested. Shared projection rebuild
logic is exercised through these integration tests but is not instrumented by
Go's default package-local coverage. Build and vet passed.
