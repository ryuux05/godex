# Embedding Godex in a service

`godex.New(ctx, Config)` assembles one chain from contract ABIs. It creates
filters and decoder routes, verifies the RPC chain identity, prepares the sink,
and loads existing progress. The lower-level `NewProcessor` / `AddChainContext`
API remains available for custom routing, address-wide event scans, and
multi-chain orchestration. Run one active indexer per chain and namespace.

## Minimal setup

With a service-owned pool and an ABI string:

```go
indexer, err := godex.New(ctx, godex.Config{
    ChainID: "1",
    RPCURL: rpcURL,
    Postgres: &godex.PostgresConfig{Pool: pool},
    Contracts: []godex.Contract{{
        Address: tokenAddress,
        ABI: tokenABI,
        Events: []string{"Transfer", "Approval"},
    }},
    FromBlock: 18_000_000,
    ConfirmationDepth: 12,
})
if err != nil { return err }
return indexer.Run(ctx)
```

The default PostgreSQL handler stores decoded events in `chronicle_events`.
It creates no application projections. Alternatively, set `Sink` to your existing
sink and omit `Postgres`; exactly one is required. Implement `WindowSink` in a
custom sink to persist complete windows atomically.

## Selection and startup checks

- Contract addresses must be 20-byte `0x` hexadecimal strings. Matching ignores
  address case. Each selected event derives both its fetch filter and route;
  overlapping routes for the same address/signature fail before indexing.
- `Events` selects names or canonical signatures. Omit it to select all events.
  Overloaded names require a signature such as `Updated(uint256)`. Unsupported
  selected event types fail; unrelated unsupported events can be left unselected.
  Selected inputs must have unique, nonempty names so their fields are accessible.
- `ChainID` accepts a nonnegative decimal or `0x` hexadecimal integer and is stored
  canonically in decimal. Endpoint identity is checked with `eth_chainId` before
  storage access. Custom RPCs can implement the optional `godex.ChainIDRPC`
  interface. `SkipChainIDCheck` explicitly opts out of verification when needed.
  The existing `godex.RPC` interface has not gained a required method.
- `cfg.Validate()` checks local configuration without network calls or database
  writes. `New` adds remote verification and persistence preparation. Optional
  handler `Validate` methods must also perform local checks only.
- Startup has an overall 30-second deadline; override `StartupTimeout` or provide
  a shorter context. The startup context is not retained by the processor.
  `Run` observes its own supplied context.

## First block and restart

`FromBlock` names the first block to index **inclusively**, unlike the legacy
`Options.StartBlock` cursor height. `FromBlock: 18_000_000` begins at block
18,000,000 in a fresh database. Zero resumes existing progress, or begins at
block 1 without a cursor. Higher stored progress takes precedence. A higher
`FromBlock` can skip forward; it does not rewind or delete existing rows.

Choose `ConfirmationDepth` explicitly for the chain. Its default is zero,
which permits unconfirmed blocks. There is no universal network finality setting.

## Tuning

Default tuning is a 1,000-block range, four fetch workers, up to eight ranges
awaiting ordered commit, one-second caught-up polling, logs mode, and a 64-block
reorg lookback. Decode errors stop the affected window by default.

`Config.Options` accepts tuning overrides. Zero range size and worker count use
defaults; other settings retain their normal `Options` defaults. Do not set its
`StartBlock`, `ConfirmationDepth`, `Addresses`, or `Topics`; configuration and
contracts own those settings. This prevents conflicting declarations.

```go
cfg.Options = &godex.Options{RangeSize: 200, FetcherConcurrency: 2}
```

`godex.DefaultOptions()` returns independent defaults, including retry settings,
for the lower-level API. `opts.Validate()` checks options locally.
`BatchSize` is deprecated and has no effect: writes commit complete windows.
Use `RangeSize` for window size and the PostgreSQL COPY threshold for write mode.

## Borrow service resources

The SDK does not close supplied pools or clients, mutate their configuration, or
install HTTP handlers in the service. Resource creation and cleanup remain with
the caller. `Status()` and `Health()` are available to the service's own handlers.

To reuse authentication, tracing or connection settings:

```go
cfg.RPCOptions = &godex.HTTPRPCOptions{
    Client: serviceHTTPClient,
    RateLimit: 20,
    BurstLimit: 5,
}
```

Providing `RPCOptions` replaces the default rate settings; zero rate disables
limiting. A nil client uses a ten-second timeout. Direct RPC calls do not retry
by themselves. During indexing, `RetryConfig.PerRequestTimeout`, the HTTP client's
timeout, and the caller's context all constrain requests; the earliest deadline
wins. Adjust both timeout settings when allowing longer requests.

For direct use, `godex.NewHTTPRPCWithOptions(endpoint, options)` validates an
HTTP/HTTPS endpoint and borrows the client. The original `NewHTTPRPC` remains
compatible. To use a custom adapter, supply `Config.RPC` and omit `RPCURL` and
`RPCOptions`.

Set `Config.Logger` and `Config.Metrics` to the service's collectors. The default
logger is `slog.Default()` and metrics are a no-op. Avoid using the same metrics
collector at both the processor and sink layers.

## Function handlers and rollback

A `postgres.HandlerFunc` adapts one event function. When writing application
projections, use `postgres.HandlerFuncs` with **both** callbacks:

```go
handler := postgres.HandlerFuncs{
    HandleEvent: func(ctx context.Context, tx pgx.Tx, ev godex.Event) error {
        value, err := ev.Fields.BigInt("value")
        if err != nil { return err }
        _, err = tx.Exec(ctx, `INSERT INTO app_transfers
            (event_id, chain_id, block_num, value) VALUES ($1,$2,$3,$4)`,
            ev.Id, ev.ChainId, ev.BlockNumber, value.String())
        return err
    },
    RollbackEvents: func(ctx context.Context, tx pgx.Tx,
        chainID string, fromBlock uint64) error {
        _, err := tx.Exec(ctx, `DELETE FROM app_transfers
            WHERE chain_id=$1 AND block_num >=$2`, chainID, fromBlock)
        return err
    },
}
cfg.Postgres = &godex.PostgresConfig{Pool: pool, Handler: handler}
```

The service owns application table migrations. Both callbacks use the sink's
transaction. Rollback starts at the requested rollback height inclusively. Recovery after
a restart may conservatively rewind beyond the actual fork because ancestor
headers are not persisted; handlers must support replaying that history. Missing
callbacks fail validation before database access; rollback is never silently
omitted. INSERT and COPY invoke event handlers only for newly stored event IDs.
External effects require their own delivery and rollback protocol; see
[production.md](production.md#handler-data-and-reorgs).

## Safe field access

`ev.Fields` offers `String`, `Bool`, `BigInt`, `Uint64`, `Int64`, and `Bytes`.
Each returns a value and an error naming an invalid or missing field. Use
`errors.Is(err, godex.ErrFieldNotFound)` to distinguish missing keys.

Integer getters accept native Go integers, `big.Int`, `*big.Int`, `json.Number`,
and decimal strings. They reject floating-point values and overflow rather than
truncate. Big integers and byte slices are copied, so modifying the result does
not modify the event. `Bytes` also accepts a `0x` hexadecimal string; `String`
reads decoded addresses without validating their address format.

When decoding stored JSON yourself, use `json.Decoder.UseNumber()` to preserve
large numbers before accessing them. The getters cannot restore precision lost
by decoding numbers into `float64`.

## Runnable service example

The [service example](../examples/service/main.go) stores selected events using
a caller-owned PostgreSQL pool and HTTP client:

```sh
export DATABASE_URL='postgres://user:password@localhost:5432/indexer?sslmode=disable'
export RPC_URL='https://your-rpc-endpoint'
export CHAIN_ID=1
export CONTRACT_ADDRESS='0xYour40HexDigitContractAddress'
export ABI_PATH=examples/erc20-indexer/erc20_abi.json
export EVENTS=Transfer,Approval
export FROM_BLOCK=18000000
export CONFIRMATION_DEPTH=12
go run ./examples/service
```

Replace the endpoint, address and database credentials with your configuration.
`EVENTS` may be omitted to select all ABI events. Starting without `FROM_BLOCK`
resumes progress or begins at block 1; missing `CONFIRMATION_DEPTH` means zero.
The database must already exist; the SDK initializes its internal tables.

Integration tests compile against the public package, verify derived routing and
startup failures, and exercise PostgreSQL INSERT/COPY, sparse-window persistence,
restart, function-based rollback and replacement-branch indexing. See
[testing.md](testing.md) for running them.
