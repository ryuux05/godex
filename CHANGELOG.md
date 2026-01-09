# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.0] - 2026-01-09

### Added
- **GoDoc Documentation**: Comprehensive GoDoc comments for all public API functions in `pkg/godex/godex.go`
- **CI/CD Pipeline**: Complete GitHub Actions workflows for automated testing, linting, and releases
  - Multi-Go version testing (1.23.x, 1.24.x)
  - Race detection and coverage reporting
  - golangci-lint integration
  - Automated cross-platform releases on version tags
- **Go Module Proxy Validation**: Workflow to ensure module proxy compatibility

### Changed
- **Public API Documentation**: Enhanced package documentation with quick start examples and API stability guarantees

### Fixed
- **SQL Column Mismatch**: Fixed INSERT statement in Uniswap swap indexer to match database schema
- **Event Routing**: Corrected topic count matching for Initialize vs Swap events (3 vs 4 indexed topics)
- **Pool Lookup Logic**: Improved handling of missing pool data with RPC fallback mechanisms
- **Schema Compatibility**: Added missing columns to `uniswap_pools` table for V4 pool data
- **Topic Filtering**: Fixed Arbitrum contract address and event signature matching
- **Test Race Conditions**: Fixed data race in processor tests using atomic operations

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

- **Cross-Chain Uniswap V4 Indexer Example**:
  - Multi-chain indexing (Ethereum + Arbitrum)
  - Uniswap V4 Swap and Initialize event handling
  - Pool state tracking and cross-chain swap connections
  - PostgreSQL persistence with optimized schemas
  - Docker Compose setup for development

- **ERC20 Indexer Example**:
  - Transfer event indexing with balance tracking
  - Multi-contract support with address filtering
  - Comprehensive PostgreSQL schema

- **PostgreSQL Sink Adapter**:
  - Transactional event persistence
  - Automatic table creation and schema management
  - Configurable bulk insert thresholds
  - Cursor state management for resumable indexing

### Changed
- **Architecture**: Modular design with clear separation of concerns
- **Error Handling**: Comprehensive error classification and retry logic
- **Performance**: Optimized concurrent processing and resource utilization

### Fixed
- **Reorg Detection**: Deterministic handling with proper ancestor validation
- **Context Cancellation**: Proper cleanup and resource management
- **Memory Usage**: LRU cache implementation for block hash storage

---

[Unreleased]: https://github.com/ryuux05/godex/compare/v0.1.0...HEAD
[v0.1.0]: https://github.com/ryuux05/godex/releases/tag/v0.1.0