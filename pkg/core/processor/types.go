package processor

import "github.com/ryuux05/godex/pkg/core/types"

type BlockRange struct {
    From uint64
    To   uint64
}

type FetchResult struct {
    Range      BlockRange
    Logs       []types.Log
    Timestamps map[uint64]uint64
    Err        error
}