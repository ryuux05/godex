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

## [v0.1.0] - 2025-01-07

### Added
- **Response Splitting**: Automatic handling of "response too big" errors with recursive binary search range splitting for RPC providers with size limits

- **Modular Architecture Refactor**:
  - Split processor into focused components: `fetcher.go`, `arbiter.go`, `planner.go`, `status.go`
  - LRU block hash cache with proper eviction for reorg detection
  - Chain progress tracking with detailed state management

- **Fetcher Enhancements**:
  - Worker pool architecture with configurable concurrency
  - Context-aware cancellation and resource cleanup
  - Batch timestamp fetching with fallback to individual calls
  - Intelligent fetch mode switching (logs vs receipts)

- **Status & Health Monitoring**:
  - `Processor.Status()` method for runtime state inspection
  - `Processor.Health()` method for health checks
  - Chain-specific status with block lag, indexed height, and live sync state
  - Structured status data for monitoring dashboards

- **Graceful Shutdown**:
  - Signal handling for SIGINT/SIGTERM
  - Clean resource cleanup and cursor persistence
  - Context-aware cancellation throughout the pipeline

- **CLI Tool**:
  - Basic scaffolding commands for new indexer projects
  - Code generation utilities for ABI handling

- **Complete ERC20 Indexer Example**:
  - Production-ready ERC20 transfer and approval indexer
  - Docker Compose setup with PostgreSQL and Prometheus
  - Custom transaction handlers with business logic
  - Health checks and metrics endpoints
  - Comprehensive README with deployment instructions

- **Integration Testing**:
  - End-to-end tests with real PostgreSQL database
  - Component interaction testing
  - Error scenario simulation

- **Benchmark Testing**:
  - Performance benchmarks for all core components
  - Memory usage profiling
  - Concurrent operation stress testing

- **Enhanced Error Handling**:
  - Structured error classification (transient, permanent, reorg)
  - Context-aware error propagation
  - Detailed error logging with correlation IDs

- **Processor**
  - Window-based HTTP log fetching with `eth_getLogs` / `eth_getBlockReceipts`
  - Per-chain `ChainInfo` and `Options` (range size, confirmations, concurrency, topics, fetch mode)
  - Arbiter-based ordered commit with reorg detection and rollback (`handleReorg`)
  - Public `Processor.Logs(chainId)` API for consuming ordered `types.Log`

- **Sink**
  - `sink.Sink` interface (`Store`, `Rollback`) for pluggable storage backends
  - Postgres sink adapter (`adapters/sink/postgres`):
    - Internal `chronicle_events` and `chronicle_cursors` schema
    - Dual insert/COPY modes with `CopyThreshold`
    - Transactional `Store` and `Rollback`
    - `Handler` interface to run user-defined schema logic in the same transaction as internal writes

- **Decoder**
  - `StandardDecoder` with ABI registration:
    - `RegisterABI(name, abiJSON)` for named ABI sets
    - `Decode(name, log)` → `*types.Event`
  - `DecoderRouter` for complex multi-contract scenarios with configurable match conditions
  - `types.Event` model with `Fields` map for decoded data

- **Metrics**
  - `metrics.Metrics` interface:
    - `IncBlocksProcessed`, `ObservedBlockLag`, `ObservedBlockFetchDuration`,
      `SetIndexedHeight`, `IncSinkWrites`, `SetProcessorConcurrency`,
      `IncSinkErrors`, `ObservedSinkWriteDuration`, `IncReorgs`
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

- **Documentation**
  - `docs/processor.md`: explanation of processor flow, windows, arbiter, and reorg strategy
  - `docs/metrics.md`: metrics reference and Prometheus usage
  - `docs/indexer_architecture.md`: high-level architecture for Processor, Sink, Metrics, and Godex
  - `docs/sink.md`: storage backend documentation
  - `docs/rpc.md`: RPC client and retry logic
  - `docs/decoder.md`: decoding strategies and ABI handling
  - Complete README with quick start, configuration, and deployment guides

### Changed
- **Architecture**: Refactored monolithic processor into modular components for better maintainability
- **Error Handling**: Enhanced with structured logging and context propagation
- **Testing**: Added comprehensive integration and benchmark tests
- **Documentation**: Comprehensive guides with real-world examples and deployment instructions

### Fixed
- Race condition in processor `isRunning` flag with proper mutex usage
- Context cancellation handling in fetch operations
- Magic number documentation and constant definitions
- Response splitting for RPC size limits
- Memory leaks in timestamp fetching and cache management

---

[Unreleased]: https://github.com/ryuux05/godex/compare/v0.1.0...HEAD
[v0.1.0]: https://github.com/ryuux05/godex/releases/tag/v0.1.0