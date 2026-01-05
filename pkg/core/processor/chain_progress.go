package processor

import (
	"time"
	"sync"
	"github.com/ryuux05/godex/pkg/core/utils"
)

type chainProgress struct {
	// Mutex to lock
	mu sync.RWMutex

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
	lastProgressAt time.Time

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
	lastProgressAt time.Time
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

func (p *chainProgress) Update(block uint64, events uint64, time time.Time) {
	p.mu.Lock()
    p.currentSyncBlock = block
	p.lastProgressAt = time
    p.eventsStored = events
	p.mu.Unlock()
}

func (p *chainProgress) SetHead(head uint64) {
    p.headBlock = head
}

func (p *chainProgress) Snapshot() snapshot {
	now := time.Now()

	// Copy state under lock
	p.mu.RLock()
	cur := p.currentSyncBlock
	head := p.headBlock

	events := p.eventsStored

	lastT := p.lastLogTime
	lastB := p.lastLogBlock
	lastE := p.lastLogEvents
	lastP := p.lastProgressAt
	p.mu.RUnlock()

	elapsed := now.Sub(lastT).Seconds()
	if elapsed <= 0 {
		elapsed = 0
	}

	// Rates
	var blocksPerSec float64
	var eventsPerSec float64
	if elapsed > 0 {
		// protect against weird ordering (shouldn't happen, but don't blow up)
		var dBlocks uint64
		if cur >= lastB {
			dBlocks = cur - lastB
		}
		var dEvents uint64
		if events >= lastE {
			dEvents = events - lastE
		}

		blocksPerSec = float64(dBlocks) / elapsed
		eventsPerSec = float64(dEvents) / elapsed
	}

	// Progress %
	var progressPct float64
	 if p.headBlock > p.syncStartBlock {
        progressPct = float64(p.currentSyncBlock) / float64(p.headBlock) * 100
    }

	// ETA
	var blocksBehind uint64
	if head > cur {
		blocksBehind = head - cur
	} else {
		blocksBehind = 0
	}

	eta := "—"
	if blocksBehind > 0 && blocksPerSec > 0 {
		etaSecs := float64(blocksBehind) / blocksPerSec
		eta = utils.FormatDuration(time.Duration(etaSecs * float64(time.Second)))
	}

	return snapshot{
		current:      cur,
		head:         head,
		events:       events,
		blockPerSec:  blocksPerSec,
		eventsPerSec: eventsPerSec,
		progressPct:  progressPct,
		eta:          eta,
		lastProgressAt: lastP,
	}
}

func (p *chainProgress) ResetLogWindow() {
	p.mu.Lock()
    p.lastLogTime = time.Now()
    p.lastLogBlock = p.currentSyncBlock
    p.lastLogEvents = p.eventsStored
	p.mu.Unlock()
}
