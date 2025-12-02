package core

import (
	"context"
	"fmt"

	"github.com/ryuux05/godex/pkg/core/metrics"
	"github.com/ryuux05/godex/pkg/core/types"
	"golang.org/x/sync/errgroup"
)

// LogToEventsFunc is the user-supplied contract: given a raw log, produce
// zero/one/many Events (usually by wrapping StandardDecoder).
type LogToEventsFunc func(log types.Log) ([]types.Event, error)

// ChainConfig describes how Godex should handle a single chain end-to-end.
type ChainConfig struct {
	Chain       ChainInfo       // RPC + identifiers
	Options     *Options        // Processor options
	Sink        Sink   // Storage backend for this chain
	LogToEvents LogToEventsFunc // Decode policy for this chain
}

// Godex is the high-level orchestrator that wires Processor + per-chain
// decoders + sinks into a running indexer.
type Godex struct {
	proc    *Processor
	chains  map[string]*ChainConfig
	metrics Metrics 
}

func NewGodex() *Godex {
    return &Godex{
        proc:    NewProcessor(metrics.Noop{}),
        chains:  make(map[string]*ChainConfig),
        metrics: metrics.Noop{},
    }
}


func NewGodexWithMetrics(m metrics.Metrics) (*Godex, error) {
	if m == nil {
		return nil, fmt.Errorf("Metrics must be specified")
	}

    return &Godex{
        proc:    NewProcessor(metrics.Noop{}),
        chains:  make(map[string]*ChainConfig),
        metrics: metrics.Noop{},
    }, nil
}


func (g *Godex) AddChain(cfg *ChainConfig) error {
	if cfg == nil {
		return fmt.Errorf("ChainConfig is required")
	}
	if cfg.Options == nil {
		return fmt.Errorf("Options is required")
	}
	if cfg.Sink == nil {
		return fmt.Errorf("Sink is required")
	}
	if cfg.LogToEvents == nil {
		return fmt.Errorf("LogToEvents is required")
	}

	if err := g.proc.AddChain(cfg.Chain, cfg.Options); err != nil {
		return err
	}

	g.chains[cfg.Chain.ChainId] = cfg
	return nil
}

func (g *Godex) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	
	errG, ctx := errgroup.WithContext(ctx)

	for chainId, cfg := range g.chains {
		chainId, cfg := chainId, cfg

		errG.Go(func() error {
			logCh, err := g.proc.Logs(chainId)
			if err != nil {
				return fmt.Errorf("logs channel for chain %s: %w", chainId, err)
			}

			const batchSize = 250
			batch := make([]types.Event, batchSize)

			flush := func() error {
				if len(batch) == 0 {
					return nil
				}
				err := cfg.Sink.Store(ctx, batch)
				batch = batch[:0]
				return err
			}

			for {
				select {
				case <- ctx.Done():
					return flush()
				case logs, ok := <-logCh:
					if !ok {
						return flush()
					}
					
					events, err := cfg.LogToEvents(logs)
					if err != nil {
						continue
					}

					if len(events) == 0 {
						continue
					}

					batch = append(batch, events...)
					if len(batch) >= batchSize {
						if err := flush(); err != nil {
							return err
						}
					}

				}
			}
		})
	}

	errG.Go(func() error {
		return g.proc.Run(ctx)
	})

	return errG.Wait()
}
