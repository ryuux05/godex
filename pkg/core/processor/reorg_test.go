package processor

import (
	"context"
	"errors"
	"testing"

	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleReorgFallback(t *testing.T) {
	for _, tc := range []struct {
		name             string
		cursor, fallback uint64
		cached           bool
		rpcFails         bool
		want             uint64
		hash             string
	}{
		{"cached fallback", 20, 8, true, true, 12, "cached-12"},
		{"RPC fallback", 20, 8, false, false, 12, "canonical"},
		{"below fallback depth", 3, 8, false, false, 0, "canonical"},
		{"RPC unavailable", 20, 8, false, true, 12, ""},
		{"empty startup", 0, 8, false, false, 0, "canonical"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, c, r, _ := newPipeline(t)
			c.cursor = &cursorState{BlockNum: tc.cursor}
			c.hardFallbackBlocks = tc.fallback
			c.blockHashCache.Set(tc.cursor+1, "orphan")
			if tc.cursor == 0 {
				c.blockHashCache.Clear()
			}
			if tc.cached {
				c.blockHashCache.Set(tc.want, tc.hash)
			}
			r.blockFn = func(context.Context, string) (types.Block, error) {
				if tc.rpcFails {
					return types.Block{}, errors.New("unavailable")
				}
				return types.Block{Hash: "canonical"}, nil
			}
			n, h := p.handleReorg(context.Background(), c)
			assert.Equal(t, tc.want, n)
			assert.Equal(t, tc.hash, h)
			_, ok := c.blockHashCache.Get(tc.cursor + 1)
			assert.False(t, ok)
		})
	}
}

func TestHandleReorgLiveWalksOneBlockBelowRangeSize(t *testing.T) {
	p, c, r, _ := newPipeline(t)
	c.opts.RangeSize = 100
	c.isLive.Store(true)
	c.cursor = &cursorState{BlockNum: 3, BlockHash: "old-3"}
	c.blockHashCache.Set(2, "hash-2")
	c.blockHashCache.Set(3, "old-3")
	var calls []string
	r.blockFn = func(_ context.Context, n string) (types.Block, error) {
		calls = append(calls, n)
		if n == "0x3" {
			return types.Block{ParentHash: "hash-2"}, nil
		}
		return types.Block{ParentHash: "new-3"}, nil
	}
	n, h := p.handleReorg(context.Background(), c)
	assert.Equal(t, uint64(2), n)
	assert.Equal(t, "hash-2", h)
	assert.Equal(t, []string{"0x4", "0x3"}, calls)
}

func TestDetectReorgRollbackFailurePreservesCursor(t *testing.T) {
	p, c, _, s := newPipeline(t)
	c.cursor = &cursorState{BlockNum: 4, BlockHash: "hash-4"}
	c.blockHashCache.Set(4, "hash-4")
	failure := errors.New("rollback failed")
	s.rollbackFn = func(context.Context, string, uint64, string) error { return failure }
	err := p.detectReorg(context.Background(), c, 5, types.Block{ParentHash: "different"})
	assert.ErrorIs(t, err, failure)
	assert.Equal(t, &cursorState{BlockNum: 4, BlockHash: "hash-4"}, c.cursor)
}

func TestCheckCursorOnResume(t *testing.T) {
	for _, tc := range []struct {
		name      string
		number    uint64
		hash      string
		wantCalls int
	}{
		{"fresh", 0, "", 0}, {"explicit start", 20, "", 0}, {"canonical", 20, "hash-20", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, c, r, _ := newPipeline(t)
			c.cursor = &cursorState{BlockNum: tc.number, BlockHash: tc.hash}
			calls := 0
			r.blockFn = func(_ context.Context, n string) (types.Block, error) {
				calls++
				assert.Equal(t, "0x14", n)
				return types.Block{Hash: tc.hash}, nil
			}
			require.NoError(t, p.checkCursorOnResume(context.Background(), c))
			assert.Equal(t, tc.wantCalls, calls)
		})
	}
	t.Run("RPC failure", func(t *testing.T) {
		p, c, r, _ := newPipeline(t)
		c.cursor = &cursorState{BlockNum: 20, BlockHash: "old"}
		failure := errors.New("unavailable")
		r.blockFn = func(context.Context, string) (types.Block, error) { return types.Block{}, failure }
		assert.ErrorIs(t, p.checkCursorOnResume(context.Background(), c), failure)
	})
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "startup rollback", true: "startup rollback failure"}[fail], func(t *testing.T) {
			p, c, r, s := newPipeline(t)
			c.cursor = &cursorState{BlockNum: 20, BlockHash: "old"}
			c.hardFallbackBlocks = 8
			r.blockFn = func(context.Context, string) (types.Block, error) {
				return types.Block{Hash: "new", ParentHash: "new-parent"}, nil
			}
			failure := errors.New("cannot rollback")
			s.rollbackFn = func(_ context.Context, _ string, n uint64, h string) error {
				assert.Equal(t, uint64(13), n)
				assert.Equal(t, "new", h)
				if fail {
					return failure
				}
				return nil
			}
			err := p.checkCursorOnResume(context.Background(), c)
			if fail {
				assert.ErrorIs(t, err, failure)
				assert.Equal(t, &cursorState{BlockNum: 20, BlockHash: "old"}, c.cursor)
			} else {
				require.NoError(t, err)
				assert.Equal(t, &cursorState{BlockNum: 12, BlockHash: "new"}, c.cursor)
			}
		})
	}
}
