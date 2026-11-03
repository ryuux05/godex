package decoder

import (
	"fmt"
	"github.com/ryuux05/godex/pkg/core/types"
	"sync"
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
	mu     sync.RWMutex
	routes []DecoderRoute
}

func NewDecoderRouter() *DecoderRouter {
	return &DecoderRouter{
		routes: make([]DecoderRoute, 0),
	}
}

// Register a decoder with a match condition
func (r *DecoderRouter) Register(match MatchFunc, abiName string, dec Decoder) *DecoderRouter {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = append(r.routes, DecoderRoute{
		Match:   match,
		Decoder: dec,
		Name:    abiName,
	})
	return r
}

// Decode implements the Decoder interface
func (r *DecoderRouter) Decode(chainId string, log types.Log) (*types.Event, error) {
	r.mu.RLock()
	routes := append([]DecoderRoute(nil), r.routes...)
	r.mu.RUnlock()
	for _, route := range routes {
		if route.Match != nil && route.Match(log) {
			return route.Decoder.Decode(route.Name, chainId, log)
		}
	}

	// No decoder matched, skip
	return nil, nil
}

// Clone snapshots routing rules so later registration does not change a chain.
func (r *DecoderRouter) Clone() *DecoderRouter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return &DecoderRouter{routes: append([]DecoderRoute(nil), r.routes...)}
}

// DecodeBatch implements the Decoder interface
func (r *DecoderRouter) DecodeBatch(logs []types.Log) (*[]types.Event, error) {
	return nil, fmt.Errorf("DecodeBatch requires chain context; use Decode")
}
