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

### Reorg Strategy (HTTP-only, window-based)

**Goal**: Detect forks without WS "removed" flags, minimize RPC calls, and roll back safely.

**What we store (arbiter-only)**:
- `storedWindowHash`: map of committed window end height → block hash (bounded ring/LRU).
- Optionally: `recentBlockHash` for last K blocks to refine ancestors (not required initially).

**Per-window attach and commit**:
- For each window [from..to] that finishes and is next to commit:
  - **Attach check**: fetch `header(from)` and require `header(from).ParentHash == storedHash[lastCommitted]`.
  - **Store end**: fetch `header(to)` and set `storedHash[to] = header(to).Hash`.
- You fetch at most 2 headers per committed window.

**Detecting reorgs**:
- **Attach fails**: `header(from).ParentHash != storedHash[lastCommitted]` → reorg detected.
- **Intra-window reorgs** (between from..to):
  - Caught next loop by either:
    - Overlap re-fetch of last K blocks via `getLogs` and noticing differences, or
    - Verifying a few recent stored (height, hash) entries against current headers.

**Lookback (find common ancestor)**:
- Cancel the current batch context to stop all in-flight RPCs.
- Walk back by window boundaries using stored end-of-window hashes:
  - Start at `ancestor = lastCommitted` (e.g., 110). Loop (bounded):
    - `child := ancestor + 1`; fetch `header(child)`.
    - If `header(child).ParentHash == storedHash[ancestor]` → ancestor found; break.
    - Else `ancestor -= RangeSize` and repeat (cap by `ReorgLookbackBlocks`).
- Optional refinement (if you keep per-block ring): step down block-by-block within the last K blocks to reduce replay.
- **Recovery**:
  - Roll back sinks to ancestor (if used).
  - Set `cursor = ancestor`; drop stored hashes > ancestor.
  - Start a new batch from `ancestor+1`.

**Why not check every block?**:
- Checking only `header(from)` and `header(to)` per window keeps header RPC usage low.
- Intra-window reorgs are caught on the next loop via overlap or stored hash verification.

**WS note**:
- If you later add WS, "removed: true" logs can trigger immediate rollback hints; HTTP header checks remain the source of truth.

8) Architecture benefits:
- **Workers**: Stateless, focus only on fetching logs concurrently.
- **Arbiter**: Stateful, ensures ordered processing and reorg safety.
- **Separation of concerns**: Fetching vs. processing/commitment logic.
- **Backpressure**: Arbiter controls pace; if output channel fills, everything waits.

9) Context & error handling:
- Honor `ctx` in all operations and loops.
- Workers send errors to `errCh`; main loop handles cancellation.
- Graceful shutdown: Wait for all workers and arbiter before exit.

**Batch lifecycle (contexts)**:
- `Run(ctx)` derives a `batchCtx` per scheduling iteration.
- On reorg or error: `batchCancel()` → wait for workers → lookback → start a fresh batch.
- Don't close the long-lived logs stream; only stop via ctx.

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

## Tuning Knobs

**Confirmations**: Process up to `head − confirmations` (or use "safe/finalized") to keep reorgs shallow/rare.

**RangeSize**: 
- Larger windows = fewer header calls, bigger rollback when reorgs happen.
- Can shrink near tip for faster reorg detection.

**ReorgLookbackBlocks** (Options): Max blocks to walk back when searching for an ancestor (e.g., 64).

**storedWindowHash capacity**: `ceil(ReorgLookbackBlocks / RangeSize) + 1`, clamped (e.g., min 8, max 256).

**OverlapBlocks** (optional): Small K (e.g., 16–64) for overlap `getLogs` on each loop.

## Message Flow
