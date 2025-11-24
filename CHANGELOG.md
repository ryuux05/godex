# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- TBD

### Changed
- TBD

### Fixed
- TBD

---

## [v0.1.0] - 2025-xx-xx

### Added
- **Processor**
  - Window-based HTTP log fetching with `eth_getLogs` / `eth_getBlockReceipts`.
  - Per-chain `ChainInfo` and `Options` (range size, confirmations, concurrency, topics, fetch mode).
  - Arbiter-based ordered commit with reorg detection and rollback (`handleReorg`).
  - Public `Processor.Logs(chainId)` API for consuming ordered `types.Log`.

- **Sink**
  - `sink.Sink` interface (`Store`, `Rollback`) for pluggable storage backends.
  - Postgres sink adapter (`adapters/sink/postgres`):
    - Internal `chronicle_events` and `chronicle_cursors` schema.
    - Dual insert/COPY modes with `CopyThreshold`.
    - Transactional `Store` and `Rollback`.
    - `Handler` interface to run user-defined schema logic in the same transaction as internal writes.

- **Decoder**
  - `StandardDecoder` with ABI registration:
    - `RegisterABI(name, abiJSON)` for named ABI sets.
    - `Decode(name, log)` → `*types.Event`.
  - `types.Event` model with `Fields` map for decoded data.

- **Metrics**
  - `metrics.Metrics` interface:
    - `IncBlocksProcessed`, `ObservedBlockLag`, `ObservedBlockFetchDuration`,
      `SetIndexedHeight`, `IncSinkWrites`, `SetProcessorConcurrency`,
      `IncSinkErrors`, `ObservedSinkWriteDuration`, `IncReorgs`.
  - Prometheus adapter (`adapters/metrics`):
    - Counters/gauges/histograms for:
      - `godex_block_processed_total`
      - `godex_block_lag`
      - `godex_block_fetched_duration_seconds`
      - `godex_indexed_block_height`
      - `godex_sink_events_writes_total`
      - `godex_sink_events_errors_total`
      - `godex_processor_concurrency`
      - `godex_reorgs_total`

- **Godex Orchestrator**
  - `core.Godex` high-level orchestrator:
    - Per-chain `ChainConfig` with `ChainInfo`, `Options`, `Sink`, `LogToEventsFunc`.
    - `AddChain` to register chains with the underlying `Processor`.
    - `Run(ctx)` to:
      - Start `Processor.Run(ctx)`.
      - For each chain: consume `Processor.Logs(chainId)`, call user-provided `LogToEvents`, batch `types.Event`, and call `Sink.Store`.
  - Default constructor `NewGodex()` using `metrics.Noop{}`.
  - Advanced constructor `NewGodexWithMetrics(m metrics.Metrics)` for custom metrics wiring.

- **Documentation**
  - `docs/processor.md`: explanation of processor flow, windows, arbiter, and reorg strategy.
  - `docs/metrics.md`: metrics reference and Prometheus usage.
  - `docs/indexer_architecture.md`: high-level architecture for Processor, Sink, Metrics, and Godex.

### Changed
- N/A (initial public release).

### Fixed
- N/A (initial public release).

---

[Unreleased]: https://github.com/ryuux05/godex/compare/v0.1.0...HEAD
[v0.1.0]: https://github.com/ryuux05/godex/releases/tag/v0.1.0