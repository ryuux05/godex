## EVM Processor (HTTP) Design

### Purpose
An event-driven blockchain indexer processor for EVM chains using HTTP JSON-RPC. It fetches logs in block ranges, routes them to decoders, writes events to sinks, and advances a cursor safely with confirmations.

### Scope
- Chain: EVM-compatible
- Transport: HTTP (eth_getLogs) first; WS optional later
- Data: Logs-first (token transfers and event logs). Transactions/receipts/traces can be added later.

## Core Concepts

### Confirmations
- Definition: Number of blocks built on top of a block before it’s considered “safe.”
- Usage: Process only up to target = head − confirmations to reduce reorg risk.
- Trade-off: Higher confirmations → lower reorg risk, higher latency.
- Examples:
  - Head=1,000,000; confirmations=12 → target=999,988 → process up to 999,988.
  - Head small (e.g., 8) and confirmations=12 → target=0 → no processing yet.

### Cursor
- Internal numeric height (uint64) of the last fully processed safe block.
- Persist externally (DB/file). The processor reads/sets it; sinks must be idempotent.

### Decoders and Sinks
- Decoder: declares `Addresses()` and `Topics()` and exposes `Decode(ctx, dctx, log) -> []Event`.
- Sink: `Write(ctx, []Event)` and `Rollback(ctx, toBlock)` to revert effects on reorgs.

### Types (SDK-facing, brief)
- Block (header-only): number, hash, parentHash, timestamp (EVM hex or numeric — the SDK keeps native hex strings and converts on demand).
- Log: address, topics, data, blockNumber/hash, txHash, indices, removed.
- Filter: addresses and topics; range added per task via fromBlock/toBlock.

## RPC Abstraction
- Head(ctx): returns the latest head (hex string or number).
- GetBlock(ctx, blockNumber): header-only; blockNumber is hex tag or quantity; returns Block.
- GetLogs(ctx, filter): returns []Log for the specified range and filter.
- Notes:
  - JSON-RPC envelope must be decoded (result vs error).
  - Providers return hex-strings for numeric fields; convert when needed.
  - Respect context and set Content-Type; check HTTP status.

## Processor Flow (HTTP, EVM)

1) Load cursor:
- Initialize from stored state or `Options.StartBlock`. Internally keep as uint64 for math.

2) Determine safe target:
- Call `Head(ctx)` → parse hex to uint64.
- Compute `target = max(0, head − Options.Confirmations)`. Exit early if `cursor >= target`.

3) Build base filter:
- Union-deduplicate addresses from all decoders.
- Merge decoder topic groups. Do not set range yet.

4) Plan ranges:
- Split `[cursor+1 .. target]` into windows of `Options.RangeSize`.

5) Concurrency (worker pool):
- Run up to `Options.MaxParralel` workers.
- For each range:
  - Clone base filter; set `FromBlock`/`ToBlock` (hex-quantity strings).
  - Call `GetLogs(ctx, filter)`.
  - Route logs to matching decoders (address and/or topic policy).
  - Call `Decode` and collect `[]Event`.
  - Write events to all sinks (all-or-error).

6) Ordered commit:
- Maintain `next = cursor+1` and track finished windows.
- Advance `cursor` only when the next contiguous window(s) have completed, committing to each window’s `to`.

7) Reorg handling (initial):
- Confirmations avoid most reorgs.
- Detection options:
  - Persist and verify block hashes for committed heights.
  - Overlap re-fetch of a small trailing range; look for `removed: true` logs.
  - Ensure parent link continuity (Block(h+1).ParentHash == Block(h).Hash).
- On reorg:
  - Find a common ancestor.
  - `Sink.Rollback(ctx, ancestorHeight)`.
  - Set `cursor = ancestorHeight` and reprocess.

8) Context & rate limiting:
- Honor `ctx` in all operations and loops.
- Rate limiting is handled by the RPC client; the processor is transport-agnostic.

## Options (selected)
- BatchSize: number of decoded events buffered per sink write.
- RangeSize: blocks per `eth_getLogs` window.
- MaxParralel: concurrent range workers.
- StartBlock: inclusive starting height (0 means derive from stored cursor).
- EndBlock: optional stop height (0 means run continuously).
- Confirmations: safety depth before processing (e.g., 5–15 for “safe” on Ethereum).
- RateLimit (RPC): enforced within the RPC client.

## Matching Policy
- Address match: if a decoder declares addresses, the log’s address must match one of them.
- Topic match: if a decoder declares topic groups, any overlap at the appropriate position counts as a match.
- Empty addresses/topics means match-all for that dimension.

## Fan-out Strategy
- Start simple: decode within the worker per range (fewer channels).
- Later: pre-index decoders by address/topic to reduce per-log checks.

## Reorg Semantics
- Removed logs: treat as signals to rollback and reprocess.
- Sinks must be idempotent: key events by block/tx/logIndex to enable clean rewrites.

## WebSocket (later)
- WS is optional for low-latency head/logs.
- Use WS as a signal; HTTP `getLogs` remains the source of truth for confirmed processing.

## Extensibility
- Transactions/Receipts: add `GetBlockWithTxs`, `GetReceipt`, or per-block fetchers, but keep logs-first in the hot path.
- Traces: add tracing APIs when indexing native/internal transfers.
- Multi-chain:
  - Keep processor API generic; EVM specifics at RPC/decoder edges.
  - Later extract a chain adapter (Head, BuildFilter, FetchRange, RouteAndDecode).

## Testing
- RPC: unit tests with `httptest.Server` for `Head`, `GetBlock`, `GetLogs` (success, JSON-RPC error, non-200).
- Processor: fakes for RPC/Decoders/Sinks to test range planning, worker pool, ordered commit, and rollback.