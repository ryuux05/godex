## Reorg Strategy (HTTP-only, window-based)
Goal
- Detect forks without WS “removed” flags, minimize RPC calls, and roll back safely.

What we store (arbiter-only)
- storedWindowHash: map of committed window end height → block hash (bounded ring/LRU).
- Optionally: recentBlockHash for last K blocks to refine ancestors (not required initially).

Per-window attach and commit
- For each window [from..to] that finishes and is next to commit:
  - Attach check: fetch header(from) and require header(from).ParentHash == storedHash[lastCommitted].
  - Store end: fetch header(to) and set storedHash[to] = header(to).Hash.
- You fetch at most 2 headers per committed window.

Detecting reorgs
- Attach fails: header(from).ParentHash != storedHash[lastCommitted] → reorg now.
- Intra-window reorgs (between from..to):
  - Caught next loop by either:
    - Overlap re-fetch of last K blocks via getLogs and noticing differences, or
    - Verifying a few recent stored (height, hash) entries against current headers.

Lookback (find common ancestor)
- Cancel the current batch context to stop all in-flight RPCs.
- Walk back by window boundaries using stored end-of-window hashes:
  - Start at ancestor = lastCommitted (e.g., 110). Loop (bounded):
    - child := ancestor + 1; fetch header(child).
    - If header(child).ParentHash == storedHash[ancestor] → ancestor found; break.
    - Else ancestor -= RangeSize and repeat (cap by ReorgLookbackBlocks).
- Optional refinement (if you keep per-block ring): step down block-by-block within the last K blocks to reduce replay.
- Recovery:
  - Roll back sinks to ancestor (if used).
  - Set cursor = ancestor; drop stored hashes > ancestor.
  - Start a new batch from ancestor+1.

Batch lifecycle (contexts)
- Run(ctx) derives a batchCtx per scheduling iteration.
- On reorg or error: batchCancel() → wait for workers → lookback → start a fresh batch.
- Don’t close the long-lived logs stream; only stop via ctx.

Tuning knobs
- Confirmations: process up to head − confirmations (or use “safe/finalized”) to keep reorgs shallow/rare.
- RangeSize: larger windows = fewer header calls, bigger rollback when reorgs happen. Can shrink near tip.
- ReorgLookbackBlocks (Options): max blocks to walk back when searching for an ancestor (e.g., 64).
- storedWindowHash capacity: ceil(ReorgLookbackBlocks / RangeSize) + 1, clamped (e.g., min 8, max 256).
- OverlapBlocks (optional): small K (e.g., 16–64) for overlap getLogs on each loop.

Why not check every block?
- Checking only header(from) and header(to) per window keeps header RPC usage low.
- Intra-window reorgs are caught on the next loop via overlap or stored hash verification.

WS note
- If you later add WS, “removed: true” logs can trigger immediate rollback hints; HTTP header checks remain the source of truth.