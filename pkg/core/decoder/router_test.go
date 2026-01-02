package decoder

import (
	"math/big"
	"testing"

	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/stretchr/testify/assert"
)

func TestRouter_ByTopicCount(t *testing.T) {
	decoder := NewStandardDecoder()
	decoder.RegisterABI("erc20", erc20Transfer_ABI)
	decoder.RegisterABI("erc721", erc721Transfer_ABI)

	router := NewDecoderRouter().
		Register(ByTopicCount(4), "erc721", decoder).
		Register(ByTopicCount(3), "erc20", decoder)

	// ERC721 log (4 topics)
	erc721Log := types.Log{
		Address: "0x1234",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
			"0x0000000000000000000000000000000000000000000000000000000000000123",
		},
		Data:        "0x",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err := router.Decode("1", erc721Log)
	assert.NoError(t, err)
	assert.NotNil(t, event)
	assert.Equal(t, "Transfer", event.EventType)
	assert.Equal(t, big.NewInt(291), event.Fields["tokenId"]) // ERC721 decoded

	// ERC20 log (3 topics)
	erc20Log := types.Log{
		Address: "0x1234",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err = router.Decode("1", erc20Log)
	assert.NoError(t, err)
	assert.NotNil(t, event)
	assert.Equal(t, "Transfer", event.EventType)
	assert.Equal(t, big.NewInt(100000000), event.Fields["value"]) // ERC20 decoded
}

func TestRouter_ByAddress(t *testing.T) {
	decoder := NewStandardDecoder()
	decoder.RegisterABI("uniswap", erc20Transfer_ABI)
	decoder.RegisterABI("erc20", erc20Transfer_ABI)

	router := NewDecoderRouter().
		Register(ByAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D"), "uniswap", decoder).
		Register(ByTopicCount(3), "erc20", decoder) // Fallback

	// Uniswap address
	uniswapLog := types.Log{
		Address: "0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err := router.Decode("1", uniswapLog)
	assert.NoError(t, err)
	assert.NotNil(t, event)
	// Should use "uniswap" ABI (though in this case both are same)

	// Different address
	otherLog := types.Log{
		Address: "0x1234567890123456789012345678901234567890",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err = router.Decode("1", otherLog)
	assert.NoError(t, err)
	assert.NotNil(t, event)
	// Should use "erc20" ABI (fallback)
}

func TestRouter_OrderMatters(t *testing.T) {
	decoder := NewStandardDecoder()
	decoder.RegisterABI("first", erc20Transfer_ABI)
	decoder.RegisterABI("second", erc20Transfer_ABI)

	// First route matches 3 topics OR address
	router := NewDecoderRouter().
		Register(ByTopicCount(3), "first", decoder).
		Register(ByAddress("0x1234"), "second", decoder)

	log := types.Log{
		Address: "0x1234",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err := router.Decode("1", log)
	assert.NoError(t, err)
	assert.NotNil(t, event)
	// Should use "first" ABI (first match wins, even though address also matches)
}

func TestRouter_NoMatch(t *testing.T) {
	decoder := NewStandardDecoder()
	decoder.RegisterABI("erc20", erc20Transfer_ABI)

	router := NewDecoderRouter().
		Register(ByTopicCount(4), "erc20", decoder)

	// Log with 2 topics (no match)
	log := types.Log{
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
		},
	}

	event, err := router.Decode("1", log)
	assert.NoError(t, err)
	assert.Nil(t, event) // No match, returns nil
}

func TestRouter_AndMatcher(t *testing.T) {
	decoder := NewStandardDecoder()
	decoder.RegisterABI("erc721", erc721Transfer_ABI)
	decoder.RegisterABI("erc20", erc20Transfer_ABI)

	router := NewDecoderRouter().
		Register(
			And(
				ByTopicCount(4),
				ByAddress("0xNFTContract"),
			),
			"erc721",
			decoder,
		).
		Register(ByTopicCount(3), "erc20", decoder)

	// Matches both conditions
	matchingLog := types.Log{
		Address: "0xNFTContract",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
			"0x0000000000000000000000000000000000000000000000000000000000000123",
		},
		Data:        "0x",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err := router.Decode("1", matchingLog)
	assert.NoError(t, err)
	assert.NotNil(t, event)
	assert.Equal(t, "Transfer", event.EventType)

	// Only matches topic count, not address
	nonMatchingLog := types.Log{
		Address: "0xOtherContract",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
			"0x0000000000000000000000000000000000000000000000000000000000000123",
		},
		Data:        "0x",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err = router.Decode("1", nonMatchingLog)
	assert.NoError(t, err)
	assert.Nil(t, event) // Doesn't match AND condition
}

func TestRouter_OrMatcher(t *testing.T) {
	decoder := NewStandardDecoder()
	decoder.RegisterABI("special", erc20Transfer_ABI)

	router := NewDecoderRouter().
		Register(
			Or(
				ByAddress("0xContract1"),
				ByAddress("0xContract2"),
			),
			"special",
			decoder,
		)

	// Matches first address
	log1 := types.Log{
		Address: "0xContract1",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err := router.Decode("1", log1)
	assert.NoError(t, err)
	assert.NotNil(t, event)

	// Matches second address
	log2 := types.Log{
		Address: "0xContract2",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err = router.Decode("1", log2)
	assert.NoError(t, err)
	assert.NotNil(t, event)

	// Doesn't match either
	log3 := types.Log{
		Address: "0xOtherContract",
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err = router.Decode("1", log3)
	assert.NoError(t, err)
	assert.Nil(t, event) // No match
}

func TestRouter_ByAddresses(t *testing.T) {
	decoder := NewStandardDecoder()
	decoder.RegisterABI("multi", erc20Transfer_ABI)

	router := NewDecoderRouter().
		Register(
			ByAddresses([]string{
				"0xContract1",
				"0xContract2",
				"0xContract3",
			}),
			"multi",
			decoder,
		)

	// Should match any of the addresses
	for _, addr := range []string{"0xContract1", "0xContract2", "0xContract3"} {
		log := types.Log{
			Address: addr,
			Topics: []string{
				"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
				"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
				"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
			},
			Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
			BlockNumber: "0x1",
			LogIndex:    "0x0",
			BlockHash:   "0xabc",
			TransactionHash: "0xdef",
		}

		event, err := router.Decode("1", log)
		assert.NoError(t, err)
		assert.NotNil(t, event, "Should match address: %s", addr)
	}
}

func TestRouter_ByTopic0(t *testing.T) {
	decoder := NewStandardDecoder()
	decoder.RegisterABI("transfer", erc20Transfer_ABI)

	transferTopic := "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

	router := NewDecoderRouter().
		Register(ByTopic0(transferTopic), "transfer", decoder)

	log := types.Log{
		Topics: []string{
			transferTopic,
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err := router.Decode("1", log)
	assert.NoError(t, err)
	assert.NotNil(t, event)

	// Different topic0
	log.Topics[0] = "0x8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e0"
	event, err = router.Decode("1", log)
	assert.NoError(t, err)
	assert.Nil(t, event) // No match
}

func TestRouter_EmptyRoutes(t *testing.T) {
	router := NewDecoderRouter()

	log := types.Log{
		Topics: []string{"0x123"},
	}

	event, err := router.Decode("1", log)
	assert.NoError(t, err)
	assert.Nil(t, event) // No routes, returns nil
}

func TestRouter_ComplexCombination(t *testing.T) {
	decoder := NewStandardDecoder()
	decoder.RegisterABI("erc721", erc721Transfer_ABI)
	decoder.RegisterABI("erc20", erc20Transfer_ABI)

	transferTopic := "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

	router := NewDecoderRouter().
		// ERC721: 4 topics + Transfer topic + specific address
		Register(
			And(
				ByTopicCount(4),
				ByTopic0(transferTopic),
				ByAddress("0xNFTContract"),
			),
			"erc721",
			decoder,
		).
		// ERC20: 3 topics + Transfer topic
		Register(
			And(
				ByTopicCount(3),
				ByTopic0(transferTopic),
			),
			"erc20",
			decoder,
		)

	// Should match ERC721 route
	erc721Log := types.Log{
		Address: "0xNFTContract",
		Topics: []string{
			transferTopic,
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
			"0x0000000000000000000000000000000000000000000000000000000000000123",
		},
		Data:        "0x",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err := router.Decode("1", erc721Log)
	assert.NoError(t, err)
	assert.NotNil(t, event)
	assert.Equal(t, "Transfer", event.EventType)

	// Should match ERC20 route
	erc20Log := types.Log{
		Address: "0xOtherContract",
		Topics: []string{
			transferTopic,
			"0x000000000000000000000000a1b2c3d4e5f6789012345678901234567890abcd",
			"0x000000000000000000000000f1e2d3c4b5a6978012345678901234567890dcba",
		},
		Data:        "0x0000000000000000000000000000000000000000000000000000000005f5e100",
		BlockNumber: "0x1",
		LogIndex:    "0x0",
		BlockHash:   "0xabc",
		TransactionHash: "0xdef",
	}

	event, err = router.Decode("1", erc20Log)
	assert.NoError(t, err)
	assert.NotNil(t, event)
	assert.Equal(t, "Transfer", event.EventType)
}
