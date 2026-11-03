package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSet(t *testing.T) {
	cache := NewBlockHashCache(10)
	cache.Set(1, "0x1")

	v, e := cache.Get(1)
	assert.True(t, e)
	assert.Equal(t, "0x1", v)
}

func TestSetExistingValue(t *testing.T) {
	cache := NewBlockHashCache(2)
	cache.Set(1, "0x1")
	cache.Set(1, "0x2")

	v, e := cache.Get(1)
	assert.True(t, e)
	assert.Equal(t, "0x2", v)
}

func TestSetOverCapacity(t *testing.T) {
	cache := NewBlockHashCache(2)
	cache.Set(1, "0x1")
	cache.Set(2, "0x2")
	cache.Set(3, "0x3")

	v, e := cache.Get(1)
	v1, e1 := cache.Get(3)
	assert.False(t, e)
	assert.Equal(t, "", v)
	assert.True(t, e1)
	assert.Equal(t, "0x3", v1)
}

func TestDropAfter(t *testing.T) {
	cache := NewBlockHashCache(3)
	cache.Set(1, "0x1")
	cache.Set(2, "0x2")
	cache.Set(3, "0x3")

	cache.DropAfter(1)
	v, e := cache.Get(2)
	assert.False(t, e)
	assert.Equal(t, "", v)
	v, e = cache.Get(3)
	assert.False(t, e)
	assert.Equal(t, "", v)
	v, e = cache.Get(1)
	assert.True(t, e)
	assert.Equal(t, "0x1", v)
}

func TestClear(t *testing.T) {
	cache := NewBlockHashCache(3)
	cache.Set(1, "0x1")
	cache.Set(2, "0x2")
	cache.Set(3, "0x3")

	cache.Clear()
	v, e := cache.Get(2)
	assert.False(t, e)
	assert.Equal(t, "", v)
	v, e = cache.Get(3)
	assert.False(t, e)
	assert.Equal(t, "", v)
	v, e = cache.Get(1)
	assert.False(t, e)
	assert.Equal(t, "", v)
}

func TestDropAfterIgnoresLRUOrder(t *testing.T) {
	cache := NewBlockHashCache(4)
	cache.Set(1, "one")
	cache.Set(2, "two")
	cache.Set(3, "three")
	cache.Get(1) // Access order differs from height order.
	cache.Set(2, "updated")
	cache.DropAfter(1)
	assert.Equal(t, 1, cache.Len())
	for _, n := range []uint64{2, 3} {
		_, ok := cache.Get(n)
		assert.False(t, ok, "orphaned height %d must be removed", n)
	}
	h, ok := cache.Get(1)
	assert.True(t, ok)
	assert.Equal(t, "one", h)
}
