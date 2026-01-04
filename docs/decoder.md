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

### DecoderRouter: Intelligent Event Routing

The DecoderRouter provides intelligent routing of blockchain logs to appropriate decoders based on configurable matching conditions. It enables complex decoding scenarios where different contracts or event types require different ABI interpretations.

#### Router Architecture

The DecoderRouter implements the same Decoder interface but internally manages multiple decoder instances and routes logs based on match conditions.

**Core Components:**
```go
type DecoderRouter struct {
    routes []DecoderRoute
}

type DecoderRoute struct {
    Match   MatchFunc    // Condition for routing
    Name    string       // ABI identifier for decoder
    Decoder Decoder      // Target decoder instance
}

type MatchFunc func(log types.Log) bool
```

#### Built-in Match Functions

The SDK provides several built-in match functions for common routing scenarios:

**ByTopicCount(count int)**
Matches logs with exactly N topics. Useful for distinguishing event variants:
```go
// ERC20 Transfer: 3 topics (Transfer(address,address,uint256))
router.Register(ByTopicCount(3), "ERC20", decoder)

// ERC721 Transfer: 4 topics (Transfer(address,address,uint256,uint256))
router.Register(ByTopicCount(4), "ERC721", decoder)
```

**ByAddress(address string)**
Matches logs from a specific contract address:
```go
router.Register(ByAddress("0xA0b86a33E6441e88C5F2712C3E9b74Ec6F6FDD6F"), "UniswapV2", decoder)
```

**ByAddresses(addresses []string)**
Matches logs from any address in the provided list:
```go
router.Register(ByAddresses([]string{
    "0xTokenA",
    "0xTokenB",
    "0xTokenC",
}), "ERC20", decoder)
```

**ByTopic0(topic0 string)**
Matches logs by event signature (first topic hash):
```go
transferTopic := "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
router.Register(ByTopic0(transferTopic), "Transfer", decoder)
```

**And(matchers ...MatchFunc)**
Combines multiple matchers with AND logic (all conditions must be true):
```go
router.Register(And(
    ByTopicCount(4),
    ByAddress("0xNFTContract"),
    ByTopic0(transferTopic),
), "ERC721", decoder)
```

**Or(matchers ...MatchFunc)**
Combines multiple matchers with OR logic (any condition must be true):
```go
router.Register(Or(
    ByAddress("0xContractA"),
    ByAddress("0xContractB"),
), "SpecialABI", decoder)
```

#### Router Configuration

**Basic Setup:**
```go
// Create router instance
router := decoder.NewDecoderRouter()

// Register routes with match conditions
router.
    Register(decoder.ByTopicCount(3), "ERC20", erc20Decoder).
    Register(decoder.ByTopicCount(4), "ERC721", erc721Decoder).
    Register(decoder.ByAddress("0xUniswap"), "DEX", dexDecoder)
```

**Complex Routing Example:**
```go
// ERC721: Must have 4 topics AND come from NFT contract AND be Transfer event
erc721Matcher := decoder.And(
    decoder.ByTopicCount(4),
    decoder.ByAddress("0xNFTContract"),
    decoder.ByTopic0("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"),
)

// ERC20: Must have 3 topics AND be Transfer event (from any contract)
erc20Matcher := decoder.And(
    decoder.ByTopicCount(3),
    decoder.ByTopic0("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"),
)

// DEX: Must come from Uniswap OR PancakeSwap
dexMatcher := decoder.Or(
    decoder.ByAddress("0xUniswapV2"),
    decoder.ByAddress("0xPancakeSwap"),
)

router := decoder.NewDecoderRouter().
    Register(erc721Matcher, "ERC721", erc721Decoder).
    Register(erc20Matcher, "ERC20", erc20Decoder).
    Register(dexMatcher, "DEX", dexDecoder)
```

#### Routing Logic and Precedence

**Route Evaluation:**
1. Routes are evaluated in registration order (first match wins)
2. For each log, the router iterates through routes sequentially
3. First route where `Match(log)` returns `true` is selected
4. If no routes match, the log is skipped (returns `nil, nil`)

**Route Ordering Matters:**
```go
// Order matters - more specific routes first
router := decoder.NewDecoderRouter().
    // Specific: Uniswap contract (checked first)
    Register(ByAddress("0xUniswap"), "Uniswap", decoder).
    // General: Any 3-topic Transfer (checked second)
    Register(ByTopicCount(3), "ERC20", decoder)
```

**Fallback Routes:**
```go
// Catch-all route (nil matcher matches everything)
router.Register(nil, "DefaultABI", decoder)

// Specific routes override fallback
router.
    Register(ByAddress("0xSpecial"), "SpecialABI", decoder).
    Register(nil, "DefaultABI", decoder)  // Fallback for non-special contracts
```

#### Router Usage with Processor

The DecoderRouter integrates seamlessly with the Processor, replacing single decoders:

```go
// Create and configure router
router := decoder.NewDecoderRouter().
    Register(decoder.ByTopicCount(3), "ERC20", standardDecoder).
    Register(decoder.ByTopicCount(4), "ERC721", standardDecoder)

// Register with processor (same as single decoder)
processor.AddChain(chainInfo, options, router)
```

**Multi-Contract Indexing Example:**
```go
// Setup decoders with different ABIs
erc20Decoder := core.NewStandardDecoder()
erc20Decoder.RegisterABI("ERC20", erc20ABI)

erc721Decoder := core.NewStandardDecoder()
erc721Decoder.RegisterABI("ERC721", erc721ABI)

dexDecoder := core.NewStandardDecoder()
dexDecoder.RegisterABI("DEX", dexABI)

// Create router with intelligent routing
router := decoder.NewDecoderRouter().
    // ERC721: 4 topics + specific NFT contracts
    Register(
        decoder.And(
            decoder.ByTopicCount(4),
            decoder.ByAddresses([]string{"0xAzuki", "0xBAYC", "0xMAYC"}),
        ),
        "ERC721",
        erc721Decoder,
    ).
    // DEX: Specific DEX contracts
    Register(
        decoder.ByAddresses([]string{"0xUniswapV2", "0xSushiSwap"}),
        "DEX",
        dexDecoder,
    ).
    // ERC20: Default for 3-topic transfers
    Register(decoder.ByTopicCount(3), "ERC20", erc20Decoder)

// Register with processor
processor.AddChain(ethereumChain, options, router)
```

**Router Benefits:**
- **Flexibility**: Handle multiple contract types in single indexer
- **Performance**: Route logs without iterating through all decoders
- **Maintainability**: Central routing logic instead of scattered conditionals
- **Extensibility**: Easy addition of new contract types and matchers

#### Custom Match Functions

Users can implement custom match functions for specialized routing logic:

```go
// Custom matcher: Block range filtering
func ByBlockRange(from, to uint64) decoder.MatchFunc {
    return func(log types.Log) bool {
        blockNum, _ := utils.HexQtyToUint64(log.BlockNumber)
        return blockNum >= from && blockNum <= to
    }
}

// Custom matcher: Data length filtering
func ByDataLength(minLength int) decoder.MatchFunc {
    return func(log types.Log) bool {
        return len(log.Data) >= minLength
    }
}

// Usage
router.Register(
    decoder.And(
        ByBlockRange(1000000, 2000000),  // Blocks 1M-2M
        ByDataLength(32),                // At least 32 bytes of data
    ),
    "HistoricalABI",
    decoder,
)
```

### Custom Decoder Implementation

Users can implement custom decoders for non-standard requirements:

**Use Cases:**
- Non-EVM chains with different encoding schemes
- Pre-filtering or post-processing logic
- Event enrichment with external data
- Performance optimization for known event structures

**Implementation Requirements:**
- Satisfy Decoder interface (Decode, DecodeBatch)
- Return Event objects with proper structure
- Handle errors gracefully

### Integration with Processor

The Processor integrates decoders directly during event processing:

```go
// Register decoder/router when adding chain
processor.AddChain(chain, options, decoder)

// Processor automatically:
// 1. Fetches logs via RPC
// 2. Calls decoder.Decode() for each log (router routes internally)
// 3. Stores resulting events via sink
```

**Key Integration Points:**
- Decoder registered per-chain during `AddChain()`
- Processor handles decoding internally for ordered, atomic processing
- Decoder errors logged but don't stop processing (resilient design)
- Router enables complex multi-contract scenarios
