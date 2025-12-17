## Decoder Architecture

### Overview

The Decoder is a pluggable component responsible for transforming raw blockchain event logs into structured, typed events. It operates independently of the Processor, allowing users to either use the provided StandardDecoder implementation or implement custom decoding logic.

### Core Responsibilities

- Parse ABI (Application Binary Interface) definitions
- Store ABIs with unique identifiers for explicit selection
- Match raw logs to event definitions via topic hash lookup
- Decode indexed parameters from log topics
- Decode non-indexed parameters from log data field
- Produce structured Event objects with typed field access

### Interface Definition

The Decoder interface defines the contract for transforming raw blockchain logs into structured events:

```go
type Decoder interface {
    // Decode transforms a single log into a structured event
    Decode(name string, chainId string, log types.Log) (*types.Event, error)

    // DecodeBatch processes multiple logs efficiently
    DecodeBatch(logs []types.Log) (*[]types.Event, error)
}
```

**Methods:**
- `Decode(name, chainId, log)`: Transforms a single log using specified ABI identifier and chain context
- `DecodeBatch(logs)`: Processes multiple logs efficiently (may use batch optimizations)

### Event Structure

Events represent the decoded output with standardized metadata and flexible field storage:

```go
type Event struct {
    // Blockchain Metadata
    ChainID      string
    BlockNumber  uint64
    BlockHash    string
    TxHash       string
    TxIndex      uint64
    LogIndex     uint64
    
    // Contract Information
    Address      string
    
    // Event Identification
    EventType    string   // "Transfer", "Approval", etc.
    EventSig     string   // "Transfer(address,address,uint256)"
    TopicHash    string   // Keccak256 hash of signature
    
    // Decoded Fields
    Fields       EventFields
    
    // Raw Log Reference
    Raw          *types.Log
}

type EventFields map[string]interface{}
```

**EventFields** provides typed accessor methods:
- `GetAddress(key string) (string, error)`
- `GetUint64(key string) (uint64, error)`
- `GetBigInt(key string) (*big.Int, error)`
- `GetBool(key string) (bool, error)`
- `GetBytes(key string) ([]byte, error)`
- `GetString(key string) (string, error)`

### StandardDecoder Implementation

StandardDecoder is the default ABI-based implementation provided by the SDK.

**Internal Structure:**
```go
type StandardDecoder struct {
    events map[string]map[string]*EventDefinition  // ABI name → topic hash → event definition
}
```

**Configuration Methods:**
- `RegisterABI(name string, abiJSON string) error` - Parse and register all events from ABI JSON with a unique identifier
- `RegisterABIFromFile(name string, filepath string) error` - Load ABI from file with a unique identifier

### ABI Registration with Identifiers

Each ABI must be registered with a unique identifier (name). This identifier is used to explicitly select which ABI to use when decoding logs. This design allows multiple ABIs with overlapping event signatures (e.g., ERC20 and ERC721 Transfer events) to coexist.

**Why Identifiers are Required:**

1. **Explicit Control**: Users explicitly choose which ABI to use for decoding, avoiding ambiguity
2. **Multiple Variants**: Different contracts may emit events with the same signature but different structures (e.g., ERC20 Transfer has 3 topics, ERC721 Transfer has 4 topics)
3. **Clear Intent**: Makes it obvious which ABI is being used for each decode operation
4. **Performance**: Direct lookup by name and topic hash (O(1)) without iteration

**Registration Example:**
```go
decoder := core.NewStandardDecoder()

// Register ERC20 ABI with identifier "ERC20"
decoder.RegisterABI("ERC20", erc20ABI)

// Register ERC721 ABI with identifier "ERC721"
decoder.RegisterABI("ERC721", erc721ABI)

// Register custom contract ABI
decoder.RegisterABI("MyContract", myContractABI)
```

**Decoding with Identifier:**
```go
// Decode using ERC20 ABI for Ethereum chain
event, err := decoder.Decode("ERC20", "1", log)

// Decode using ERC721 ABI for Polygon chain
event, err := decoder.Decode("ERC721", "137", log)

// Batch decode (ABI selection handled internally)
events, err := decoder.DecodeBatch(logs)
```

### ABI Requirements

The decoder requires full event definitions, not just signatures. Each event definition must include:

1. **Event name** - Human-readable identifier
2. **Parameter names** - Used as keys in Event.Fields map
3. **Parameter types** - Determines decoding logic (address, uint256, etc.)
4. **Indexed status** - Identifies which parameters are in topics vs data field

**Example ABI Event Definition:**
```json
{
  "name": "Transfer",
  "type": "event",
  "inputs": [
    {"name": "from", "type": "address", "indexed": true},
    {"name": "to", "type": "address", "indexed": true},
    {"name": "value", "type": "uint256", "indexed": false}
  ]
}
```

### Decoding Process

1. **ABI Selection**: User specifies which ABI identifier to use via `DecodeWith(name, log)`
2. **Topic Hash Lookup**: Extract `topics[0]` and lookup event definition in the specified ABI registry
3. **Structure Validation**: Verify log structure matches event definition (topic count, data presence)
4. **Indexed Parameter Decoding**: Decode `topics[1..n]` based on indexed parameter types
5. **Data Field Decoding**: Parse log data field for non-indexed parameters
6. **Field Population**: Combine decoded values into EventFields map
7. **Event Construction**: Return structured Event with metadata and fields

### Type Mapping (Solidity to Go)

| Solidity Type | Go Type | Storage |
|---------------|---------|---------|
| address | string | topics or data |
| uint8-uint64 | uint64 | topics or data |
| uint256 | *big.Int | topics or data |
| int8-int64 | int64 | topics or data |
| int256 | *big.Int | topics or data |
| bool | bool | topics or data |
| bytes, bytesN | []byte | data only |
| string | string | data only |
| arrays | []T | data only |

### Handling Event Variants

Different contracts may emit events with the same signature but different structures. For example:

- **ERC20 Transfer**: `Transfer(address indexed, address indexed, uint256)` - 3 topics, value in data
- **ERC721 Transfer**: `Transfer(address indexed, address indexed, uint256 indexed)` - 4 topics, tokenId in topics

Both have the same topic hash (`0xddf252ad...`) but different structures. By registering each with a unique identifier, users can explicitly choose which variant to use:

```go
decoder.RegisterABI("ERC20", erc20TransferABI)   // 3 topics expected
decoder.RegisterABI("ERC721", erc721TransferABI) // 4 topics expected

// User explicitly selects which variant to use
event, err := decoder.DecodeWith("ERC20", log)   // Uses ERC20 structure
event, err := decoder.DecodeWith("ERC721", log)  // Uses ERC721 structure
```

### Multi-Chain Support

StandardDecoder is chain-agnostic and can be shared across multiple EVM chains:

```go
decoder := core.NewStandardDecoder()
decoder.RegisterABI("ERC20", erc20ABI)  // Works on all EVM chains

processor.AddChain(ethereumChain, &Options{Decoder: decoder})
processor.AddChain(polygonChain, &Options{Decoder: decoder})
```

**Rationale**: Standard Solidity events use identical encoding across all EVM-compatible chains.

### Multi-Contract Support

A single decoder can handle multiple contracts with different ABIs, each registered with a unique identifier:

```go
decoder := core.NewStandardDecoder()
decoder.RegisterABI("USDC", usdcABI)        // ERC20 events
decoder.RegisterABI("Uniswap", uniswapABI) // DEX events
decoder.RegisterABI("NFT", nftABI)         // ERC721 events

// Decode with explicit ABI selection
usdcEvent, _ := decoder.DecodeWith("USDC", log)
uniswapEvent, _ := decoder.DecodeWith("Uniswap", log)
```

### Custom Decoder Implementation

Users can implement custom decoders for non-standard requirements:

**Use Cases:**
- Non-EVM chains with different encoding schemes
- Pre-filtering or post-processing logic
- Event enrichment with external data
- Performance optimization for known event structures

**Implementation Requirements:**
- Satisfy Decoder interface (DecodeWith, DecodeWithBatch, GetTopics)
- Return Event objects with proper structure
- Handle errors gracefully

### Integration with Processor

The Processor integrates decoders directly during event processing:

```go
// Register decoder when adding chain
processor.AddChain(chain, options, decoder)

// Processor automatically:
// 1. Fetches logs via RPC
// 2. Calls decoder.Decode() for each log
// 3. Stores resulting events via sink
```

**Key Integration Points:**
- Decoder registered per-chain during `AddChain()`
- Processor handles decoding internally for ordered, atomic processing
- Decoder errors logged but don't stop processing (resilient design)
