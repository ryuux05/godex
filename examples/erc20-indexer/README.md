# ERC20 Indexer Example

A complete example demonstrating how to index ERC20 Transfer and Approval events on Ethereum mainnet using the godex SDK.

## Features

- **Automatic Decoding**: Events decoded using ERC20 ABI
- **Custom Handler**: Business logic runs atomically with event storage
- **Batch Storage**: Events stored efficiently in PostgreSQL
- **Reorg Handling**: Automatic rollback on blockchain reorganizations
- **Metrics Export**: Prometheus metrics for monitoring
- **Graceful Shutdown**: Handles SIGINT/SIGTERM signals

## Architecture

```
┌─────────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐
│   RPC       │────►│ Processor│────►│  Decoder │────►│   Sink   │
│ (Ethereum)  │     │          │     │ (ERC20)  │     │(Postgres)│
└─────────────┘     └──────────┘     └──────────┘     └────┬─────┘
                                                             │
                                                             ▼
                                                      ┌──────────┐
                                                      │ Handler  │
                                                      │(Custom)  │
                                                      └──────────┘
```

## Setup

### 1. Prerequisites

- Go 1.21 or later
- Docker and Docker Compose
- Ethereum RPC endpoint (Alchemy, Infura, or local node)

### 2. Environment Variables

Create a `.env` file or export variables:

```bash
export RPC_URL="https://eth-mainnet.g.alchemy.com/v2/YOUR_API_KEY"
export DATABASE_URL="postgres://godex:password@localhost:5432/godex?sslmode=disable"
export START_BLOCK=18000000  # Optional: default is 18M
```

### 3. Start Infrastructure

```bash
docker-compose up -d
```

Wait for PostgreSQL to be ready (healthcheck will verify).

### 4. Initialize Database Schema

The sink automatically creates internal tables. For custom handler tables:

```bash
psql postgres://godex:password@localhost:5432/godex -f schema.sql
```

### 5. Run Indexer

```bash
cd examples/erc20-indexer
go run main.go
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
6. **Handles** reorgs by rolling back orphaned events
7. **Exports** metrics for monitoring

## Database Schema

### Internal Tables (Created Automatically)

- `chronicle_events`: All decoded events
- `chronicle_cursors`: Processing progress per chain

### Custom Tables (Created by Handler)

- `erc20_transfer_stats`: Transfer event statistics
- `erc20_approvals`: Approval event records
- `erc20_balances`: Token holder activity tracking

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
- **Performance**: Single database round-trip

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
opts := &core.Options{
    RangeSize:              1000,  // Blocks per batch
    FetcherConcurrency:    4,     // Concurrent workers
    StartBlock:             18000000,
    ConfirmationDepth:     12,    // Wait for confirmations
    EnableTimestamps:       true, // Include block timestamps
    Topics: []string{
        "0xddf252ad...", // Transfer
        "0x8c5be1e5...", // Approval
    },
}
```

### RPC Configuration

```go
rpc := core.NewHTTPRPC(
    "https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY",
    20, // requests per second
    5,  // burst capacity
)
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
    event_type,
    address as contract,
    block_number,
    transaction_hash,
    fields
FROM chronicle_events
WHERE chain_id = '1'
  AND event_type IN ('Transfer', 'Approval')
ORDER BY block_number DESC
LIMIT 100;
```

## Stopping

Send SIGINT (Ctrl+C) for graceful shutdown. The indexer will:
- Complete current batch processing
- Update cursor position
- Close database connections
- Exit cleanly

## Troubleshooting

### Database Connection Errors

```bash
# Verify PostgreSQL is running
docker-compose ps

# Check connection
psql postgres://godex:password@localhost:5432/godex -c "SELECT 1"
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

## Next Steps

- Add more event types (ERC721, custom contracts)
- Implement balance tracking logic
- Add real-time notifications
- Scale to multiple chains

## License

See [LICENSE](../../LICENSE) file.

