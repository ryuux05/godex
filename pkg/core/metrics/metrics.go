package metrics

import "time"

type Metrics interface {
	IncBlocksProcessed(chainId string, n uint64)
	ObservedBlockLag(chainId string, lag uint64)
	ObservedBlockFetchDuration(chainId string, d time.Duration, success bool)
	SetIndexedHeight(chainId string, height uint64)
	IncSinkWrites(chainId string, n uint64)
	SetProcessorConcurrency(chainId string, n uint64)
	IncSinkErrors(chainId string)
	ObservedSinkWriteDuration(chainId string, d time.Duration, success bool)
	IncReorgs(chainId string)
}

// No operation type
// A dummy implementation of interface that do nothing
type Noop struct{}

func (Noop) IncBlocksProcessed(string, uint64)                              {}
func (Noop) ObservedBlockLag(string, uint64)                                 {}
func (Noop) ObservedSinkWriteDuration(string, time.Duration, bool)           {}
func (Noop) IncSinkWrites(string, uint64)                                      {}
func (Noop) IncSinkErrors(string)                                           {}
func (Noop) SetIndexedHeight(string, uint64)                                {}
func (Noop) ObservedBlockFetchDuration(string, time.Duration, bool)          {}
func (Noop) SetProcessorConcurrency(string, uint64)                            {}
func (Noop) IncReorgs(string)  												{}