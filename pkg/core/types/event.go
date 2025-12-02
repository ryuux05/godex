package types

// Internal representation (what you store in StandardDecoder)
type EventDefinition struct {
    Name      string        // "Transfer"
    Signature string        // "Transfer(address,address,uint256)"
    TopicHash string        // "0xddf252ad..." (computed)
    Inputs    []EventInput  // Parsed inputs
}

type EventInput struct {
    Name    string   // "from"
    Type    string   // "address"
    Indexed bool     // true
}

type Event struct {
	// Indempotent key to prevent retries/replays
	Id string `json:"id"`
	ChainId string `json:"chainId"`
	// Block that emits the event
	BlockNumber uint64 `json:"blockNumber"`
	// The hash of the block that emits this event
	BlockHash string `json:"blockHash"`
	// Which contract that emits this event
	Address string `json:"address"`
	// Hash of transaction where produce the event
	TransactionHash string `json:"transactionHash"`
	// The integer of the log index position in the block.
	LogIndex uint64 `json:"logIndex"`
	Timestamp uint64 `json:"timestamp"`
	// Event function name
	EventType string `json:"EventType"`
	// The output of the log
	Fields EventFields
}

type EventFields map[string]interface{}