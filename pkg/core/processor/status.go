package processor

import (
	"time"
	"fmt"
)

type ProcessorStatus struct {
	IsRunning   bool                    `json:"is_running"`
	Chains      map[string]ChainStatus  `json:"chains"`
	StartTime   time.Time               `json:"start_time"`
	TotalEvents uint64                  `json:"total_events"`
}

type ChainStatus struct {
	ChainId             string    `json:"chain_id"`
	Name                string    `json:"name"`
	IsRunning           bool      `json:"is_running"`
	IsLive              bool      `json:"is_live"`

	CursorBlock         uint64    `json:"cursor_block"`
	CursorHash          string    `json:"cursor_hash"`
	HeadBlock           uint64    `json:"head_block"`

	BlocksBehind        uint64    `json:"blocks_behind"`
	ProgressPct         float64   `json:"progress_pct"`
	BlocksPerSec        float64   `json:"blocks_per_sec"`
	EventsTotal         uint64    `json:"events_total"`
	EventsPerSec        float64   `json:"events_per_sec"`
	ETA                 string    `json:"eta"`

	LastProgressAt      time.Time `json:"last_progress_at"`
	LastError           string    `json:"last_error"`
	LastErrorAt         time.Time `json:"last_error_at"`

	ConfirmationDepth   uint64    `json:"confirmation_depth"`
	RangeSize           int       `json:"range_size"`
	FetcherConcurrency  int       `json:"fetcher_concurrency"`
	DecoderConcurrency  int       `json:"decoder_concurrency"`
}

type HealthReport struct {
	Healthy   bool            `json:"healthy"`
	IsRunning bool            `json:"is_running"`
	Chains    map[string]bool `json:"chains"`
	Reasons   []string        `json:"reasons"`
}

func (p *Processor) Status() ProcessorStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	status := ProcessorStatus{
		IsRunning: p.isRunning,
		Chains:    make(map[string]ChainStatus),
	}
	
	for chainId, chain := range p.chains {
		snap := chain.progress.Snapshot()

		// avoid underflow
		var behind uint64
		if snap.head > snap.current {
			behind = snap.head - snap.current
		}

		cs := ChainStatus{
			ChainId:   chain.chainInfo.ChainId,
			Name:      chain.chainInfo.Name,
			IsRunning: p.isRunning,
			IsLive:    chain.isLive,

			// commited cursor
			CursorBlock: chain.cursor.BlockNum,
			CursorHash:  chain.cursor.BlockHash,

			HeadBlock: snap.head,

			BlocksBehind: behind,
			ProgressPct:  snap.progressPct,
			BlocksPerSec: snap.blockPerSec,

			EventsTotal:  snap.events,
			EventsPerSec: snap.eventsPerSec,
			ETA:          snap.eta,

			LastProgressAt: snap.lastProgressAt,

			LastError:   chain.lastErr,   // string
			LastErrorAt: chain.lastErrAt, // time.Time

			ConfirmationDepth:  chain.opts.ConfirmationDepth,
			RangeSize:          chain.opts.RangeSize,
			FetcherConcurrency: chain.opts.FetcherConcurrency,
		}

		status.Chains[chainId] = cs
	}

	return status
}

func (p *Processor) Health() HealthReport {
	const stallThreshold = 2 * time.Minute

	now := time.Now()

	p.mu.RLock()
	defer p.mu.RUnlock()
	
	report := HealthReport{
		Healthy: true,
		IsRunning: p.isRunning,
		Chains: make(map[string]bool, len(p.chains)),
		Reasons : []string{},
	}

	if !p.isRunning {
		report.Healthy = false
		report.Reasons = append(report.Reasons, "processor is not running")
	}

	if len(p.chains) == 0 {
		report.Healthy = false
		report.Reasons = append(report.Reasons, "no chain registered")
		return report
	}

	for chainID, chain := range p.chains {
		snap := chain.progress.Snapshot()

		ok := true

		// If you track last error per chain:
		if chain.lastErr != "" {
			ok = false
			report.Reasons = append(report.Reasons, fmt.Sprintf("[%s] last error: %s", chainID, chain.lastErr))
		}

		// stalled progress check (only meaningful when running)
		if p.isRunning && !snap.lastProgressAt.IsZero() {
			if now.Sub(snap.lastProgressAt) > stallThreshold {
				ok = false
				report.Reasons = append(report.Reasons, fmt.Sprintf("[%s] stalled for %s", chainID, now.Sub(snap.lastProgressAt)))
			}
		}

		report.Chains[chainID] = ok
		if !ok {
			report.Healthy = false
		}
	}

	return report

}

