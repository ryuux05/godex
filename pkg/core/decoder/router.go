package decoder

import (
	"github.com/ryuux05/godex/pkg/core/types"
)

// MatchFunc determines if a decoder should be used for a given log
type MatchFunc func(log types.Log) bool

// DecoderRoute pairs a matcher with a decoder
type DecoderRoute struct {
	// The condition that needs to be fulfill
	Match MatchFunc
	// Name of the abi that registered in decoder
	Name string
	// The decoder after the condition is fulfilled
	Decoder Decoder
}

type DecoderRouter struct {
	routes []DecoderRoute
}

func NewDecoderRouter() *DecoderRouter {
	return &DecoderRouter{
		routes: make([]DecoderRoute, 0),
	}
}

// Register a decoder with a match condition
func (r *DecoderRouter) Register(match MatchFunc, abiName string, dec Decoder) *DecoderRouter {
	r.routes = append(r.routes, DecoderRoute{
		Match:   match,
		Decoder: dec,
		Name:    abiName,
	})
	return r
}

// Decode implements the Decoder interface
func (r *DecoderRouter) Decode(chainId string, log types.Log) (*types.Event, error) {
	for _, route := range r.routes {
		if route.Match != nil && route.Match(log) {
			return route.Decoder.Decode(route.Name, chainId, log)
		}
	}

	// No decoder matched, skip
	return nil, nil
}

// DecodeBatch implements the Decoder interface
func (r *DecoderRouter) DecodeBatch(logs []types.Log) (*[]types.Event, error) {
	return nil, nil
}
