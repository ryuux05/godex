# Generate typed Go handlers

Generate Go event types, parsers, and PostgreSQL callbacks from a JSON ABI.
The generated types expose fields such as `transfer.From` and `transfer.Value`
while retaining the original event metadata separately.

## Generate a binding file

From the repository root:

```sh
go run ./cmd/godex generate \
  -abi examples/erc20-indexer/erc20_abi.json \
  -package token \
  -type ERC20 \
  -event Transfer \
  -out bindings/token/erc20_gen.go
```

Change `-abi` and `-out` to use your own ABI and destination. The input can be a
JSON ABI array or a compiler artifact with an `abi` array. `-package` names the
Go package. `-type` prefixes exported identifiers so several contracts can share
a package without their Transfer types colliding.

Omit `-event` to generate all events. Repeat it to select several:

```sh
go run ./cmd/godex generate \
  -abi examples/erc20-indexer/erc20_abi.json \
  -package token -type ERC20 \
  -event Transfer -event Approval \
  -out bindings/token/erc20_gen.go
```

The file contains:

```go
type ERC20Transfer struct {
	From  godex.Address
	To    godex.Address
	Value *big.Int
}
```

It also contains `ERC20ABI`, `ERC20Contract(address)`, `ParseERC20Transfer`, and
`ERC20Handlers` with an `OnTransfer` callback. Selecting Approval adds its event
type, parser, and callback too.

Generation validates the selected events before writing output. It creates
parent directories and replaces an existing Godex-generated file atomically.
Handwritten files and symlink destinations are rejected. Use `-out -` to write
source to stdout. Output has no timestamps or absolute source paths, so repeated
generation produces the same bytes.

## Use typed callbacks

Import your generated package and supply callbacks for every selected event:

```go
handlers := token.ERC20Handlers{
	OnTransfer: func(ctx context.Context, tx pgx.Tx,
		meta godex.Event, transfer token.ERC20Transfer) error {
		slog.Info("transfer",
			"from", transfer.From,
			"to", transfer.To,
			"value", transfer.Value.String(),
			"chain", meta.ChainId,
			"block", meta.BlockNumber)
		return nil
	},
}
```

Use the generated contract helper and handler in your existing configuration:

```go
cfg.Contracts = []godex.Contract{token.ERC20Contract(contractAddress)}
cfg.Postgres = &godex.PostgresConfig{Pool: pool, Handler: handlers}
```

The callback receives the sink's context and transaction. `meta` contains the
original event ID, chain, contract, transaction, block, and log index. The typed
payload contains copied integers and byte slices, so modifying it does not
modify the raw event fields.

Generated dispatchers validate required callbacks and event fields. Missing
callbacks, unsupported event names, malformed fields, and integer overflow return
errors before application callbacks run. A callback's error propagates to the
sink, which rolls back its transaction.

When callbacks write application projections, pair the dispatcher with your
existing rollback function:

```go
if err := handlers.Validate(); err != nil {
	return err
}
cfg.Postgres.Handler = postgres.HandlerFuncs{
	HandleEvent:    handlers.Handle,
	RollbackEvents: rollbackPayments,
}
```

Generated dispatchers implement `postgres.Handler`; they do not invent rollback
logic for application tables. The [payment reconciler](../examples/payment-reconciler/handler.go)
uses generated `ERC20Transfer` values and retains its transactional rollback.

For another sink, call `ParseERC20Transfer(rawEvent)` to obtain the typed payload
without using the PostgreSQL dispatcher.

## Map Solidity types to Go

| Solidity event field | Generated Go type |
| --- | --- |
| `address` | `godex.Address`, normalized to lowercase |
| `bool` | `bool` |
| `uint8`, `uint16`, `uint32`, `uint64` | Corresponding Go unsigned integer |
| `int8`, `int16`, `int32`, `int64` | Corresponding Go signed integer |
| Other supported integer widths, including `uint24` and `uint256` | `*big.Int`, checked against the declared width |
| Non-indexed `string` | `string` |
| Non-indexed `bytes` | `[]byte` |
| `bytes32` | `[32]byte` |
| Indexed `string` or `bytes` | `[32]byte` containing the topic hash |

Indexed dynamic values contain hashes rather than their original contents, as
defined by the [Solidity ABI specification](https://docs.solidity.org/en/latest/abi-spec.html#events).
Integer aliases `uint` and `int` normalize to `uint256` and `int256`.

Unnamed parameters receive positional names such as `Arg0`. Use the generated
contract helper so runtime decoding uses the same normalized names. Conflicting
Go identifiers fail generation with an error.

Arrays, tuples, anonymous events, and fixed byte sizes other than bytes32 remain
unsupported by the current decoder and fail generation when selected. Unselected
events and non-event ABI entries do not affect generation.

An event name can have only one selected signature in a generated binding.
For overloaded events, select one using its canonical signature:

```sh
go run ./cmd/godex generate -abi contract.json -package token -type Token \
  -event 'Changed(uint256)' -out bindings/token/changed_gen.go
```

Use the generated contract helper and keep other overloads out of that
dispatcher's input. The current decoded `godex.Event` records an event name
rather than its signature, so one dispatcher cannot distinguish several
overloads of that name.

## Regenerate with go generate

The payment reconciler includes a `go:generate` directive. Regenerate its
committed binding from the repository root:

```sh
go generate ./examples/payment-reconciler
```

Keep generated files in version control. Generation requires only the ABI file;
it does not connect to an RPC endpoint or database. The normal test suite checks
that the payment binding matches its ABI and the current generator.
