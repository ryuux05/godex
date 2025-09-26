## Processor Flow (HTTP, EVM)

1) Load cursor:
- Initialize from stored state or `Options.StartBlock`. Internally keep as uint64 for math.

2) Determine safe target:
- Call `Head(ctx)` → parse hex to uint64.
- Compute `target = max(0, head − Options.Confirmations)`. Exit early if `cursor >= target`.

3) Build topics filter:
- Use configured topics from `Options.Topics` (supports both function signatures and direct hashes).
- Function signatures are automatically converted to Keccak256 hashes.

4) Plan ranges:
- Split `[cursor+1 .. target]` into windows of `Options.RangeSize`.

5) Concurrent fetching (worker pool):
- Run up to `Options.FetcherConcurrency` workers.
- Each worker:
  - Receives block ranges from a jobs channel.
  - Creates filter with `FromBlock`/`ToBlock` (hex-quantity strings) and configured topics.
  - Calls `GetLogs(ctx, filter)` to fetch raw logs.
  - Sends results to arbiter via `doneCh` (does NOT commit or process logs).

6) Arbiter-based ordered commit:
- Single arbiter goroutine handles all log processing and commitment.
- Maintains `next = cursor+1` and tracks finished windows in maps:
  - `window[from] = to` - tracks completed ranges
  - `windowLogs[from] = []Log` - stores logs for each range
- **Sequential processing**: Only processes contiguous windows starting from `next`.
- For each ready window:
  a) **Reorg detection**: Fetch block header and verify parent hash continuity.
  b) **Log commitment**: Send logs to output channel (`p.logsCh`) in order.
  c) **Cursor advancement**: Update `cursor = end` and `next = end + 1`.
  d) **Hash storage**: Store window end block hash for future reorg detection.

7) Reorg handling:
- **Detection**: Compare `Block(next).ParentHash` with stored `storedWindowHash[next-1]`.
- **On mismatch**:
  - Cancel current batch processing.
  - Call `handleReorg(ctx)` to find common ancestor.
  - Rollback cursor to ancestor and restart processing.
- **Ancestor search**: Walk backwards through stored window hashes up to `storedWindowHashCap`.
- **Fallback**: If ancestor not found, fallback by `hardFallbackBlocks` (default: 1000).

8) Architecture benefits:
- **Workers**: Stateless, focus only on fetching logs concurrently.
- **Arbiter**: Stateful, ensures ordered processing and reorg safety.
- **Separation of concerns**: Fetching vs. processing/commitment logic.
- **Backpressure**: Arbiter controls pace; if output channel fills, everything waits.

9) Context & error handling:
- Honor `ctx` in all operations and loops.
- Workers send errors to `errCh`; main loop handles cancellation.
- Graceful shutdown: Wait for all workers and arbiter before exit.

## Options (current implementation)
- **RangeSize**: blocks per `eth_getLogs` window.
- **FetcherConcurrency**: concurrent fetcher workers.
- **StartBlock**: inclusive starting height (0 means derive from stored cursor).
- **Confirmations**: safety depth before processing (e.g., 5–15 for "safe" on Ethereum).
- **LogsBufferSize**: buffer size for the output logs channel.
- **Topics**: array of function signatures or direct hashes for log filtering.
- **ReorgLookbackBlocks**: maximum blocks to walk back during reorg detection.

## Key Data Structures
- **Jobs channel**: Distributes block ranges to fetcher workers.
- **Done channel**: Signals completion of all fetchers to main loop.
- **DoneCh**: Carries fetched logs from workers to arbiter.
- **Window maps**: Track completion status and store logs per range.
- **StoredWindowHash**: Cache of block hashes for reorg detection.

## Message Flow