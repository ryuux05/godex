# Godex CLI

The `godex` CLI tool provides utilities to help you bootstrap and manage your Godex indexer projects.

## Installation

```bash
go install github.com/ryuux05/godex@latest
```

## Commands

### `godex init`

Initialize a new Godex indexer project in the current directory. This command generates boilerplate code to help you get started quickly.

```bash
godex init
```

**Generated files:**
- `main.go`: A template indexer implementation.
- `go.mod`: Go module definition.
- `.env`: Environment variables template.
- `README.md`: Basic project instructions.

### `godex gen type`

Generate Go types from an ABI JSON file. This is useful for creating type-safe bindings for your smart contract events.

```bash
godex gen type --abi <path/to/abi.json>
```

**Flags:**
- `--abi`: Path to the ABI JSON file (required).
- `--out`: Path where the type will be generated.

**Example:**

```bash
godex gen type --abi ./abis/erc20.json --out ./internal/types
```

This will output the generated Go structs to standard output or a file if configured, allowing you to easily integrate them into your indexer logic.
