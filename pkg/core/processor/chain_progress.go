package processor

import (
	"time"

	"github.com/ryuux05/godex/pkg/core/utils"
)

type chainProgress struct {
	// Sync time state
	syncStartTime  time.Time
	syncStartBlock uint64

	// Current time state
	currentSyncBlock uint64
	eventsStored uint64

	// Last log state
	lastLogTime   time.Time
	lastLogBlock  uint64
	lastLogEvents uint64

	// Head block
	headBlock uint64
}

type snapshot struct {
	current uint64
	head uint64 
	events uint64
	blockPerSec float64
	eventsPerSec float64
	progressPct float64
	eta string
}

func NewChainProgress(startBlock uint64) *chainProgress {
	now := time.Now()
	return &chainProgress{
		syncStartTime:   now,
		syncStartBlock:  startBlock,
		lastLogTime:     now,
		lastLogBlock:    startBlock,
		currentSyncBlock: startBlock,
	}
}

func (p *chainProgress) Update(block uint64, events uint64) {
    p.currentSyncBlock = block
    p.eventsStored = events
}

func (p *chainProgress) SetHead(head uint64) {
    p.headBlock = head
}

func (p *chainProgress) Snapshot() snapshot {
    now := time.Now()
    elapsed := now.Sub(p.lastLogTime).Seconds()
    
	var blocksPerSec float64
	var eventsPerSec float64
	var progressPct float64
	var eta string

    if elapsed > 0 {
        blocksPerSec = float64(p.currentSyncBlock-p.lastLogBlock) / elapsed
        eventsPerSec = float64(p.eventsStored-p.lastLogEvents) / elapsed
    }
    
    if p.headBlock > p.syncStartBlock {
        progressPct = float64(p.currentSyncBlock) / float64(p.headBlock) * 100
    }
    
    blocksBehind := p.headBlock - p.currentSyncBlock
    if blocksPerSec > 0 {
        etaSecs := float64(blocksBehind) / blocksPerSec
        eta = utils.FormatDuration(time.Duration(etaSecs) * time.Second)
    } else {
        eta = "—"
    }
    
	return snapshot{
		current: p.currentSyncBlock,
		head: p.headBlock,
		events: p.eventsStored,
		blockPerSec: blocksPerSec,
		eventsPerSec: eventsPerSec,
		progressPct: progressPct,
		eta: eta,
	}
}

func (p *chainProgress) ResetLogWindow() {
    p.lastLogTime = time.Now()
    p.lastLogBlock = p.currentSyncBlock
    p.lastLogEvents = p.eventsStored
}