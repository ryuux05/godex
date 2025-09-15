package core

const ZeroAddress Address = "0x0000000000000000000000000000000000000000"

type Block struct {
	// Current block number
	Number uint64
	// The hash of the block
	Hash string
	// The previous block hash
	ParentHash string
	// The time the block is created
	Timestamp uint64
}

type Address string

type Log struct {
	// An address from which this log originated
	Address string
	// An array of zero to four 32 Bytes DATA of indexed log arguments. 
	// In Solidity, the first topic is the hash of the signature of the event (e.g. Deposit(address, bytes32, uint256)), except you declare the event with the anonymous specifier
	Topics string
	// It contains one or more 32 Bytes non-indexed arguments of the log
	Data string
	// The block number where this log was in. null when it's a pending log
	BlockNumber string
	// The hash of the transactions this log was created from. null when its a pending log
	TransactionHash string
	// The integer of the transaction's index position that the log was created from. null when it's a pending log
	TransactionIndex int32
	// The hash of the block where this log was in. null when it's a pending log
	BlockHash string
	// The integer of the log index position in the block. null when it's a pending log
	LogIndex string
	// The integer of the log index position in the block. null when it's a pending log
	Removed string
}

type Event struct {

}

type Filter struct {
	// The block number as a string in hexadecimal format or tags.
	FromBlock string
	// The block number as a string in hexadecimal format or tags.
	ToBlock string
	// The contract address or a list of addresses from which logs should originate
	Address []string
	// An array of DATA topics and also, the topics are order-dependent. Visit this official page to learn more about topics
	Topics []string
	// Using the blockHash field is equivalent to setting the fromBlock and toBlock to the block number the blockHash references. If blockHash is present in the filter criteria, neither fromBlock nor toBlock is allowed
	BlockHash string
}

type Cursor struct {

}

type DecodeContext struct {

}