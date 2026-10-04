# ERC20 Indexer Example

A complete example demonstrating how to index ERC20 Transfer and Approval events on Ethereum mainnet using the godex SDK.

## Features

- **Automatic Decoding**: Events decoded using ERC20 ABI
- **Custom Handler**: Business logic runs atomically with event storage
- **Batch Storage**: Events stored efficiently in PostgreSQL
- **Reorg Handling**: Automatic rollback on blockchain reorganizations
- **Metrics Export**: Prometheus metrics for monitoring
- **Graceful Shutdown**: Handles SIGINT/SIGTERM signals
- **Docker Ready**: Single command to run everything



## Quick Start with Docker

The fastest way to run the example - everything in containers.

### 1. Configure Environment

Create a `.env` file:

```bash
RPC_URL=https://eth-mainnet.g.alchemy.com/v2/YOUR_API_KEY
START_BLOCK=18000000
```

### 2. Start Everything

```bash
docker-compose up -d
```

This single command will:
- Start PostgreSQL and wait for it to be healthy
- Automatically initialize the database schema
- Build and run the indexer container

### 3. View Logs

```bash
# All services
docker-compose logs -f

# Just the indexer
docker-compose logs -f indexer

# Just PostgreSQL
docker-compose logs -f postgres
```

### 4. Stop Services

```bash
docker-compose down
```

### 5. Clean Everything (including data)

```bash
docker-compose down -v
```

## Local Development Setup

For development, you may want to run the indexer locally while keeping PostgreSQL in Docker.

### 1. Prerequisites

- Go 1.24.7 or later
- Docker and Docker Compose
- Ethereum RPC endpoint (Alchemy, Infura, or local node)

### 2. Environment Variables

```bash
export RPC_URL="https://eth-mainnet.g.alchemy.com/v2/YOUR_API_KEY"
export DATABASE_URL="postgres://godex:password@localhost:5432/godex?sslmode=disable"
export START_BLOCK=18000000  # Optional: indexing begins at START_BLOCK + 1
```

### 3. Start PostgreSQL Only

```bash
docker-compose up -d postgres
```

Wait for PostgreSQL to be ready (healthcheck will verify).

### 4. Initialize Database Schema

The example initializes both internal and application tables at startup. No
manual schema step is needed for a fresh database.

### 5. Run Indexer Locally

```bash
cd examples/erc20-indexer
go run .
```

## What It Does

1. **Connects** to Ethereum mainnet via HTTP RPC
2. **Fetches** logs for ERC20 Transfer and Approval events
3. **Decodes** events using the ERC20 ABI
4. **Stores** structured events in PostgreSQL (automatic)
5. **Processes** events via custom handler (atomic):
   - Stores transfer statistics
   - Tracks token holder activity
   - Records approval events
6. **Handles** reorgs by removing orphaned events and approvals and restoring holder activity from surviving transfers in the same transaction
7. **Exports** metrics for monitoring

## Database Schema

### Internal Tables (Created Automatically)

- `chronicle_events`: All decoded events
- `chronicle_cursors`: Processing progress per chain

### Application Tables (Initialized at Startup)

- `erc20_transfer_stats`: Transfer event statistics
- `erc20_approvals`: Approval event records
- `erc20_balances`: Last-transfer height per chain, contract, and holder; this is activity tracking, not token balance amounts

## Handler Pattern

The `ERC20Handler` runs within the same database transaction as event storage:

```go
BEGIN;
  INSERT INTO chronicle_events ...;      -- Store decoded event
  INSERT INTO erc20_transfer_stats ...;  -- Handler logic
  UPDATE chronicle_cursors ...;           -- Update cursor
COMMIT;
```

**Benefits:**
- **Atomicity**: All operations succeed or fail together
- **Consistency**: No orphaned data
- **Replay safety**: Previously stored event IDs do not rerun handler writes

## Monitoring

### Metrics

The indexer exports Prometheus metrics:

- `godex_blocks_processed_total{chain_id="1"}` - Total blocks indexed
- `godex_block_lag{chain_id="1"}` - Blocks behind chain head
- `godex_sink_writes_total{chain_id="1"}` - Storage operations
- `godex_sink_errors_total{chain_id="1"}` - Storage failures
- `godex_reorgs_total{chain_id="1"}` - Reorganizations detected

### Logs

Structured JSON logs include:
- Chain information
- Block processing progress
- Error details
- Reorg detection

## Configuration

### Indexing Options

```go
opts := &godex.Options{
    RangeSize:          1000,     // Blocks per batch
    FetcherConcurrency: 4,        // Concurrent workers
    StartBlock:         18000000,
    ConfirmationDepth:  12,       // Wait for confirmations
    EnableTimestamps:   true,     // Include block timestamps
    Topics: [][]string{{
        "0xddf252ad...", // Transfer (use the complete topic hash)
        "0x8c5be1e5...", // Approval (use the complete topic hash)
    }},
}
```

### RPC Configuration

```go
rpc := godex.NewHTTPRPC(
    "https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY",
    20, // requests per second
    5,  // burst capacity
)
```

## Docker Configuration

### File Structure

```
examples/erc20-indexer/
├── dockerfile           # Multi-stage build for indexer
├── docker-compose.yml   # PostgreSQL + Indexer services
├── main.go              # Indexer application
├── rollback.go          # Transactional application recovery
├── erc20_abi.json       # ERC20 event definitions
├── schema.sql           # Custom handler tables
└── README.md            # This file
```

### Customizing docker-compose.yml

```yaml
services:
  indexer:
    environment:
      RPC_URL: ${RPC_URL}                    # Your RPC endpoint
      DATABASE_URL: postgres://...           # Database connection
      START_BLOCK: ${START_BLOCK:-18000000}  # Starting block
    depends_on:
      postgres:
        condition: service_healthy           # Wait for DB
    restart: unless-stopped                  # Auto-restart on failure
```

### Building the Image Manually

```bash
# From repository root
docker build -t godex-erc20-indexer -f examples/erc20-indexer/dockerfile .
```

## Querying Data

### Recent Transfers

```sql
SELECT 
    contract_address,
    from_address,
    to_address,
    value,
    block_num,
    tx_hash
FROM erc20_transfer_stats
ORDER BY block_num DESC
LIMIT 100;
```

### Token Holder Activity

```sql
SELECT 
    contract_address,
    holder_address,
    last_transfer_block
FROM erc20_balances
WHERE contract_address = '0x...'
ORDER BY last_transfer_block DESC;
```

### Event History

```sql
SELECT 
    kind,
    address as contract,
    block_num,
    tx_hash,
    payload
FROM chronicle_events
WHERE chain_id = '1'
  AND event_type IN ('Transfer', 'Approval')
ORDER BY block_number DESC
LIMIT 100;
```

## Upgrading an Existing Database

Older application tables lack the provenance required for rollback. Stop the
indexer, then start the updated example once with `REBUILD_PROJECTIONS=1`.
This recreates application tables from stored `chronicle_events` while preserving
internal events and cursors. Schema replacement and replay are atomic. Remove
the flag after successful preparation so later restarts resume normally.

With Docker Compose, add the flag to `.env` for that startup, then remove it and
recreate the indexer service. Keep the PostgreSQL volume. The rebuild requires
complete retained event history; application row IDs and creation timestamps
can change. See [production operation](../../docs/production.md#upgrading-existing-example-databases)
for recovery limits and large-history costs.

## Stopping

Send SIGINT (Ctrl+C) for graceful shutdown. The indexer will:
- Cancel outstanding work
- Keep the last successfully committed window cursor
- Close database connections
- Exit cleanly

For Docker:
```bash
docker-compose down      # Stop containers
docker-compose down -v   # Stop and remove volumes
```

## Troubleshooting

### Database Connection Errors

```bash
# Verify PostgreSQL is running
docker-compose ps

# Check connection
psql postgres://godex:password@localhost:5432/godex -c "SELECT 1"

# View PostgreSQL logs
docker-compose logs postgres
```

### Indexer Not Starting

```bash
# Check indexer logs
docker-compose logs indexer

# Verify environment variables
docker-compose config
```

### RPC Rate Limiting

If you hit rate limits:
- Reduce `FetcherConcurrency`
- Increase rate limit in RPC configuration
- Use a premium RPC provider

### High Memory Usage

- Reduce `RangeSize` for smaller batches
- Lower `FetcherConcurrency` for fewer workers
- Monitor with `godex_processor_concurrency` metric

### Container Build Issues

```bash
# Rebuild without cache
docker-compose build --no-cache

# Check build logs
docker-compose build indexer
```

## Next Steps

- Add more event types (ERC721, custom contracts)
- Implement balance tracking logic
- Add real-time notifications
- Scale to multiple chains

## License

See [LICENSE](../../LICENSE) file.
