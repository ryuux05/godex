package decoder

import (
	"github.com/ryuux05/godex/pkg/core/types"
)

type Decoder interface {
	// Decode is a function to transform log into strcutural event
	Decode(name string, chainId string, log types.Log) (*types.Event, error)
	// Batch decoding
	DecodeBatch(logs []types.Log) (*[]types.Event, error)
}