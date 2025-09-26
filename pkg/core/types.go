package core

const ZeroAddress Address = "0x0000000000000000000000000000000000000000"

type Block struct {
	// Current block number
	Number string
	// The hash of the block
	Hash string
	// The previous block hash
	ParentHash string
	// The time the block is created
	Timestamp string
}

type Address string

type Log struct {
	// An address from which this log originated
	Address string `json:"address,omitempty"`
	// An array of zero to four 32 Bytes DATA of indexed log arguments. 
	// In Solidity, the first topic is the hash of the signature of the event (e.g. Deposit(address, bytes32, uint256)), except you declare the event with the anonymous specifier
	Topics []any `json:"topics,omitempty"`
	// It contains one or more 32 Bytes non-indexed arguments of the log
	Data string `json:"data,omitempty"`
	// The block number where this log was in. null when it's a pending log
	BlockNumber string `json:"blockNumber,omitempty"`
	// The hash of the transactions this log was created from. null when its a pending log
	TransactionHash string `json:"transactionHash,omitempty"`
	// The integer of the transaction's index position that the log was created from. null when it's a pending log
	TransactionIndex string `json:"transactionIndex,omitempty"`
	// The hash of the block where this log was in. null when it's a pending log
	BlockHash string `json:"blockHash,omitempty"`
	// The integer of the log index position in the block. null when it's a pending log
	LogIndex string `json:"logIndex,omitempty"`
	// The integer of the log index position in the block. null when it's a pending log
	Removed bool `json:"removed,omitempty"`
}

type Event struct {

}

type Filter struct {
	// The block number as a string in hexadecimal format or tags.
	FromBlock string `json:"fromBlock"`
	// The block number as a string in hexadecimal format or tags.
	ToBlock string `json:"toBlock"`
	// The contract address or a list of addresses from which logs should originate
	Address []string `json:"address,omitempty"`
	// An array of DATA topics and also, the topics are order-dependent.
	// Topics can be either:
	// - Function signatures like "Transfer(address,address,uint256)"
	// - Keccal256 hashes like "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	// - Keccak256 hashes only like "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	Topics []string `json:"topics,omitempty"`   // positional; omit if unused
	// Using the blockHash field is equivalent to setting the fromBlock and toBlock to the block number the blockHash references. If blockHash is present in the filter criteria, neither fromBlock nor toBlock is allowed
	BlockHash string `json:"blockHash,omitempty"`
}

type Cursor struct {

}

type DecodeContext struct {

}