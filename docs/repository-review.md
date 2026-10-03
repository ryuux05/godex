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

## Remaining priorities

These findings are follow-up work, not assertions that the current suite covers
every failure mode.

| Priority | Finding and source | Recommended next work |
| --- | --- | --- |
| High | `PGSink.copyInternalEvents` rejects duplicate IDs, while `insertInternalEvents` ignores them. INSERT still invokes handlers for replayed IDs. | Define replay semantics for both internal events and handler effects; test replay across the COPY threshold before introducing conflict-safe bulk insertion. |
| High | `Processor.addChain` divides by `RangeSize` without validation and stores caller-owned options. Nil dependencies, nonpositive concurrency, invalid fetch modes, and zero-attempt retry configs are not rejected consistently. | Validate configuration before cursor loading; test invalid options and snapshot mutable configuration. |
| High | `StandardDecoder.Decode` silently skips field decode failures, while `DecodeBatch` is a no-op in both standard and router decoders. Indexed dynamic values, arrays, tuples, and additional integer widths need explicit support decisions. Narrow unsigned values are checked against uint64, not their declared width. | Define unsupported/malformed data behavior, enforce ABI widths, and build fixture tests for each supported Solidity type. |
| Medium | `runChain` immediately repeats `processBatch` when planning finds no work. | Add configurable polling/backoff and test request counts during idle live operation and prompt cancellation. |
| Medium | Reorg recovery updates the cursor but does not reset the progress snapshot. `lastErr` is retained after a recovered reorg; `ProcessorStatus.StartTime` and `TotalEvents` are never populated. | Define health recovery semantics; test snapshots immediately after rollback, aggregate counters, and recovery after transient errors. |
| Medium | `processWindow` advances its in-memory cursor to the window end, while a nonempty sink batch persists the last event's block. Empty trailing blocks in that window can be replayed after restart. | Define an atomic events-plus-window-cursor operation and test restart after a sparse window. |
| Medium | `GetChain` and `IsLive` access the chain map without a lock; unknown `GetChain` IDs panic. Registration can replace an existing chain ID. | Define lookup and registration behavior; add concurrent registration/read tests and duplicate/unknown ID cases. |
| Medium | `PGSink.Store` updates only the final event's chain cursor and assumes sorted events. `schema_internal.sql` creates unnamed indexes on every initialization. | Enforce single-chain ordered batches or update each chain explicitly; make schema initialization idempotent and test repeated construction. |
| Medium | The full suite still does not inject transaction begin/commit failures, HTTP null results, all malformed batch envelopes, or very deep reorg budget exhaustion. | Add focused fault injection and a processor-plus-PostgreSQL reorg/restart scenario. |
| Low | `Makefile` build/run/migrate targets reference absent `cmd` paths; README quick-start decoder wiring differs from the exported router API. Examples remain untested. | Align library build targets and docs with the public SDK smoke test; add tests for example handlers and release packaging. |

## Validation

The expanded suite was run with race detection and a real isolated PostgreSQL
database. Build and `go vet` checks were run. All three fuzz targets ran for five
seconds each. Concurrent failure, cancellation, and status tests were repeated
30 times under the race detector. CI YAML was parsed with duplicate-key detection
and checked for its service, DSN, and race-test step; the hosted GitHub workflow
itself has not been executed during this local review.

Commands and database setup are documented in [testing.md](testing.md).
