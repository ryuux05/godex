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