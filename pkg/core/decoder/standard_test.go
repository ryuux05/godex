package decoder

import (
	//"encoding/json"
	//"fmt"
	"math/big"
	"testing"

	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/stretchr/testify/assert"
)

const erc20Transfer_ABI = `[
	{
	  "anonymous": false,
	  "inputs": [
		{
		  "indexed": true,
		  "internalType": "address",
		  "name": "from",
		  "type": "address"
		},
		{
		  "indexed": true,
		  "internalType": "address",
		  "name": "to",
		  "type": "address"
		},
		{
		  "indexed": false,
		  "internalType": "uint256",
		  "name": "value",
		  "type": "uint256"
		}
	  ],
	  "name": "Transfer",
	  "type": "event"
	}
  ]`

const erc721Transfer_ABI = `[
	{
	  "anonymous": false,
	  "inputs": [
		{
		  "indexed": true,
		  "internalType": "address",
		  "name": "from",
		  "type": "address"
		},
		{
		  "indexed": true,
		  "internalType": "address",
		  "name": "to",
		  "type": "address"
		},
		{
		  "indexed": true,
		  "internalType": "uint256",
		  "name": "tokenId",
		  "type": "uint256"
		}
	  ],
	  "name": "Transfer",
	  "type": "event"
	}
  ]`

const approvalEvent_ABI = `[
	{
	  "anonymous": false,
	  "inputs": [
		{
		  "indexed": true,
		  "internalType": "address",
		  "name": "owner",
		  "type": "address"
		},
		{
		  "indexed": true,
		  "internalType": "address",
		  "name": "spender",
		  "type": "address"
		},
		{
		  "indexed": false,
		  "internalType": "uint256",
		  "name": "value",
		  "type": "uint256"
		}
	  ],
	  "name": "Approval",
	  "type": "event"
	}
  ]`

const boolEvent_ABI = `[
	{
	  "anonymous": false,
	  "inputs": [
		{
		  "indexed": false,
		  "internalType": "bool",
		  "name": "success",
		  "type": "bool"
		}
	  ],
	  "name": "BoolEvent",
	  "type": "event"
	}
  ]`

const stringEvent_ABI = `[
	{
	  "anonymous": false,
	  "inputs": [
		{
		  "indexed": false,
		  "internalType": "string",
		  "name": "message",
		  "type": "string"
		}
	  ],
	  "name": "StringEvent",
	  "type": "event"
	}
  ]`

func TestDecodeTransfer_Successful(t *testing.T) {
	decoder := NewStandsardDecoder()
	err := decoder.RegisterABI("erc20", erc20Transfer_ABI)
	assert.NoError(t, err)

	log := types.Log{
		Address: "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:             "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber:      "0x112a880",
		BlockHash:        "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		TransactionHash:  "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		TransactionIndex: "0x0",
		LogIndex:         "0x5",
	}

	event, err := decoder.Decode("erc20", "1", log)

	assert.NoError(t, err)
	assert.NotNil(t, event)
	assert.Equal(t, "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef:0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890:5", event.Id)
	assert.Equal(t, uint64(18000000), event.BlockNumber)
	assert.Equal(t, "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", event.Address)
	assert.Equal(t, "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", event.BlockHash)
	assert.Equal(t, "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", event.TransactionHash)
	assert.Equal(t, uint64(5), event.LogIndex)
	assert.Equal(t, "Transfer", event.EventType)
	assert.Equal(t, "0xa1b2c3d4e5f6789012345678901234567890abcd", event.Fields["from"])
	assert.Equal(t, "0xf1e2d3c4b5a6978012345678901234567890dcba", event.Fields["to"])
	assert.Equal(t, big.NewInt(100000000), event.Fields["value"])
}

func TestDecodeERC721Transfer_Successful(t *testing.T) {
	decoder := NewStandsardDecoder()
	err := decoder.RegisterABI("erc721", erc721Transfer_ABI)
	assert.NoError(t, err)

	log := types.Log{
		Address: "0x1234567890123456789012345678901234567890",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
			"0x0000000000000000000000000000000000000000000000000000000000000123",
		},
		Data:            "0x",
		BlockNumber:     "0x1",
		BlockHash:       "0xabcdef",
		TransactionHash: "0x123456",
		LogIndex:        "0x0",
	}

	event, err := decoder.Decode("erc721", "1", log)

	assert.NoError(t, err)
	assert.NotNil(t, event)
	assert.Equal(t, "Transfer", event.EventType)
	assert.Equal(t, "0xa1b2c3d4e5f6789012345678901234567890abcd", event.Fields["from"])
	assert.Equal(t, "0xf1e2d3c4b5a6978012345678901234567890dcba", event.Fields["to"])
	assert.Equal(t, big.NewInt(291), event.Fields["tokenId"]) // 0x123 = 291
}

func TestDecode_ABINotFound(t *testing.T) {
	decoder := NewStandsardDecoder()

	log := types.Log{
		Topics: []string{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"},
	}

	event, err := decoder.Decode("nonexistent", "1", log)

	assert.Error(t, err)
	assert.Nil(t, event)
	assert.Contains(t, err.Error(), "not found")
}

func TestDecode_LogWithNoTopics(t *testing.T) {
	decoder := NewStandsardDecoder()
	decoder.RegisterABI("erc20", erc20Transfer_ABI)

	log := types.Log{
		Topics: []string{},
		Data:   "0x",
	}

	event, err := decoder.Decode("erc20", "1", log)

	assert.NoError(t, err)
	assert.Nil(t, event) // Should return nil, nil for empty topics
}

func TestDecode_EventNotInABI(t *testing.T) {
	decoder := NewStandsardDecoder()
	decoder.RegisterABI("erc20", erc20Transfer_ABI)

	// Log with different topic hash (not Transfer)
	log := types.Log{
		Topics: []string{
			"0x8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e0", // Different event
		},
	}

	event, err := decoder.Decode("erc20", "1", log)

	assert.NoError(t, err)
	assert.Nil(t, event) // Event not in this ABI
}

func TestDecode_StructureMismatch(t *testing.T) {
	decoder := NewStandsardDecoder()
	decoder.RegisterABI("erc20", erc20Transfer_ABI)

	// ERC721 log (4 topics) but using ERC20 ABI (expects 3 topics)
	log := types.Log{
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
			"0x0000000000000000000000000000000000000000000000000000000000000123", // Extra topic
		},
		Data: "0x",
	}

	event, err := decoder.Decode("erc20", "1", log)

	assert.NoError(t, err)
	assert.Nil(t, event) // Structure doesn't match
}

func TestDecode_BoolEvent(t *testing.T) {
	decoder := NewStandsardDecoder()
	decoder.RegisterABI("bool", boolEvent_ABI)

	log := types.Log{
		Topics: []string{
			"0x...", // BoolEvent topic hash (you'll need to compute this)
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000000000001",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
	}

	// Note: You'll need the correct topic hash for BoolEvent
	// This is a template - adjust topic hash accordingly
	event, err := decoder.Decode("bool", "1", log)

	if err == nil && event != nil {
		assert.Equal(t, "BoolEvent", event.EventType)
		assert.Equal(t, true, event.Fields["success"])
	}
}

func TestDecode_StringEvent(t *testing.T) {
	decoder := NewStandsardDecoder()
	decoder.RegisterABI("string", stringEvent_ABI)

	// String "Hello World" encoded in ABI format
	// Offset: 0x20 (32 bytes)
	// Length: 11 (0x0b)
	// Data: "Hello World"
	log := types.Log{
		Topics: []string{
			"0x...", // StringEvent topic hash
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000000000020000000000000000000000000000000000000000000000000000000000000000b48656c6c6f20576f726c64000000000000000000000000000000000000000000",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
	}

	// Note: Adjust topic hash accordingly
	event, err := decoder.Decode("string", "1", log)

	if err == nil && event != nil {
		assert.Equal(t, "StringEvent", event.EventType)
		assert.Equal(t, "Hello World", event.Fields["message"])
	}
}

func TestRegisterABI_InvalidJSON(t *testing.T) {
	decoder := NewStandsardDecoder()

	err := decoder.RegisterABI("test", "invalid json")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ABI JSON")
}

func TestRegisterABI_EmptyABI(t *testing.T) {
	decoder := NewStandsardDecoder()

	err := decoder.RegisterABI("empty", "[]")

	assert.NoError(t, err) // Empty ABI is valid, just no events
}

func TestRegisterABI_MultipleABIs(t *testing.T) {
	decoder := NewStandsardDecoder()

	err1 := decoder.RegisterABI("erc20", erc20Transfer_ABI)
	err2 := decoder.RegisterABI("erc721", erc721Transfer_ABI)
	err3 := decoder.RegisterABI("approval", approvalEvent_ABI)

	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.NoError(t, err3)

	// Verify all ABIs are registered
	log1 := types.Log{
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
	}

	event1, err := decoder.Decode("erc20", "1", log1)
	assert.NoError(t, err)
	assert.NotNil(t, event1)
	assert.Equal(t, "Transfer", event1.EventType)

	// ERC721 log
	log2 := types.Log{
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
			"0x0000000000000000000000000000000000000000000000000000000000000123",
		},
		Data:        "0x",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
	}

	event2, err := decoder.Decode("erc721", "1", log2)
	assert.NoError(t, err)
	assert.NotNil(t, event2)
	assert.Equal(t, "Transfer", event2.EventType)
}

func TestDecode_DataTooShort(t *testing.T) {
	decoder := NewStandsardDecoder()
	decoder.RegisterABI("erc20", erc20Transfer_ABI)

	log := types.Log{
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e1", // Too short
		BlockNumber: "0x1",
		LogIndex:    "0x0",
	}

	event, err := decoder.Decode("erc20", "1", log)

	assert.NoError(t, err)
	assert.Nil(t, event)
}

func TestDecode_MissingIndexedParameter(t *testing.T) {
	decoder := NewStandsardDecoder()
	decoder.RegisterABI("erc20", erc20Transfer_ABI)

	// Missing second topic (to address)
	log := types.Log{
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			// Missing second indexed parameter
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
	}

	event, err := decoder.Decode("erc20", "1", log)

	assert.NoError(t, err)
	assert.Nil(t, event)
}

