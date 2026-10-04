package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChainProgressSnapshot(t *testing.T) {
	p := NewChainProgress(100)
	p.SetHead(200)
	now := time.Now()
	p.lastLogTime = now.Add(-10 * time.Second)
	p.Update(150, 20, now)
	s := p.Snapshot()
	assert.Equal(t, uint64(150), s.current)
	assert.Equal(t, uint64(200), s.head)
	assert.Equal(t, uint64(20), s.events)
	assert.InDelta(t, 5, s.blockPerSec, 0.1)
	assert.InDelta(t, 2, s.eventsPerSec, 0.1)
	assert.Equal(t, float64(75), s.progressPct)
	assert.NotEqual(t, "—", s.eta)
	assert.Equal(t, now, s.lastProgressAt)
	p.ResetLogWindow()
	s = p.Snapshot()
	assert.Zero(t, s.blockPerSec)
	assert.Zero(t, s.eventsPerSec)
}

func TestChainProgressSnapshotBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		current, head uint64
		future        bool
	}{
		{"no head", 0, 0, false}, {"ahead of head", 200, 100, false}, {"rollback", 50, 200, false}, {"nonpositive interval", 150, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewChainProgress(100)
			p.lastLogBlock = 100
			p.lastLogEvents = 10
			p.lastLogTime = time.Now().Add(-time.Second)
			if tc.future {
				p.lastLogTime = time.Now().Add(time.Hour)
			}
			p.SetHead(tc.head)
			p.Update(tc.current, 5, time.Now())
			s := p.Snapshot()
			assert.GreaterOrEqual(t, s.blockPerSec, float64(0))
			assert.Zero(t, s.eventsPerSec)
			if tc.current < 100 || tc.future {
				assert.Zero(t, s.blockPerSec)
			}
			if tc.head <= tc.current || s.blockPerSec == 0 {
				assert.Equal(t, "—", s.eta)
			}
		})
	}
}

func TestStatusReportsChainState(t *testing.T) {
	p, c, _, _ := newPipeline(t)
	now := time.Now()
	p.isRunning = true
	c.isLive.Store(true)
	c.cursor = &cursorState{BlockNum: 12, BlockHash: "hash-12"}
	c.progress.SetHead(20)
	c.progress.Update(12, 7, now)
	c.lastErr = "temporary failure"
	c.lastErrAt = now
	s := p.Status()
	assert.True(t, s.IsRunning)
	assert.Len(t, s.Chains, 1)
	cs := s.Chains["1"]
	assert.Equal(t, "test", cs.Name)
	assert.Equal(t, "1", cs.ChainId)
	assert.True(t, cs.IsLive)
	assert.Equal(t, uint64(12), cs.CursorBlock)
	assert.Equal(t, "hash-12", cs.CursorHash)
	assert.Equal(t, uint64(20), cs.HeadBlock)
	assert.Equal(t, uint64(8), cs.BlocksBehind)
	assert.Equal(t, uint64(7), cs.EventsTotal)
	assert.Equal(t, float64(60), cs.ProgressPct)
	assert.Equal(t, now, cs.LastProgressAt)
	assert.Equal(t, "temporary failure", cs.LastError)
	assert.Equal(t, now, cs.LastErrorAt)
	assert.Equal(t, 2, cs.RangeSize)
	assert.Equal(t, 2, cs.FetcherConcurrency)
	// Callers may modify their returned map without modifying processor state.
	delete(s.Chains, "1")
	assert.Len(t, p.Status().Chains, 1)
	c.progress.SetHead(10)
	assert.Zero(t, p.Status().Chains["1"].BlocksBehind)
}

func TestHealthReportsFailures(t *testing.T) {
	t.Run("empty stopped processor", func(t *testing.T) {
		h := NewProcessor(nil, NoopSink{}).Health()
		assert.False(t, h.Healthy)
		assert.False(t, h.IsRunning)
		assert.Contains(t, h.Reasons, "processor is not running")
		assert.Contains(t, h.Reasons, "no chain registered")
	})
	for _, tc := range []struct {
		name         string
		running      bool
		lastErr      string
		lastProgress time.Time
		healthy      bool
		reason       string
	}{
		{"healthy", true, "", time.Now(), true, ""},
		{"starting", true, "", time.Time{}, true, ""},
		{"stopped", false, "", time.Now(), false, "processor is not running"},
		{"error", true, "database unavailable", time.Now(), false, "last error: database unavailable"},
		{"stalled", true, "", time.Now().Add(-3 * time.Minute), false, "stalled for"},
		{"stopped old progress", false, "", time.Now().Add(-3 * time.Minute), false, "processor is not running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, c, _, _ := newPipeline(t)
			p.isRunning = tc.running
			c.progress.SetHead(10)
			c.lastErr = tc.lastErr
			c.progress.Update(0, 0, tc.lastProgress)
			h := p.Health()
			assert.Equal(t, tc.healthy, h.Healthy)
			assert.Equal(t, tc.running, h.IsRunning)
			if tc.reason != "" {
				require.NotEmpty(t, h.Reasons)
				assert.Contains(t, h.Reasons[0], tc.reason)
			} else {
				assert.Empty(t, h.Reasons)
			}
		})
	}
}
