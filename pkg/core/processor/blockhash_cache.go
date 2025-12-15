package processor

import (
	"container/list"
)

// BlockHashCache is an LRU cache for storing block hashes.
// Used for reorg detection by comparing parent hashes.
type BlockHashCache struct {
	capacity int
	items map[uint64]*list.Element
	order *list.List
}

type cacheEntry struct {
	blockNum uint64
	hash     string
}

func NewBlockHashCache(capacity int) *BlockHashCache {
	return &BlockHashCache{
		capacity: capacity,
		items: make(map[uint64]*list.Element, capacity),
		order: list.New(),
	}
}

// Set stores a block hash. If the block already exists, it updates the hash
// and moves it to the back (most recent). If capacity is exceeded, evicts the oldest.
func (c *BlockHashCache) Set(blockNum uint64, hash string) {
	// rewrite if exists, and move to back
	if elem, exists := c.items[blockNum]; exists {
		elem.Value.(*cacheEntry).hash = hash
		c.order.MoveToBack(elem)
		return
	}

	// if over-capacity
	if c.order.Len() >= c.capacity {
		oldest := c.order.Front()
		if oldest != nil {
			entry := oldest.Value.(*cacheEntry)
			delete(c.items, entry.blockNum)
			c.order.Remove(oldest)
		}
	}

	entry := &cacheEntry{blockNum: blockNum, hash: hash}
	elem := c.order.PushBack(entry)
	c.items[blockNum] = elem
}

// Get retrieves a block hash. Returns the hash and true if found.
func (c *BlockHashCache) Get(blockNum uint64) (string, bool) {
	if elem, exists := c.items[blockNum]; exists {
		return elem.Value.(*cacheEntry).hash, true
	}
	return "", false
}

// DropAfter removes all entries with blockNum > after.
// Used during reorg to invalidate orphaned block hashes.
func (c *BlockHashCache) DropAfter(after uint64) {
	for elem := c.order.Back(); elem != nil; {
		entry := elem.Value.(*cacheEntry)
		if entry.blockNum <= after {
			break
		}
		prev := elem.Prev()
		delete(c.items, entry.blockNum)
		c.order.Remove(elem)
		elem = prev
	}
}

// Len returns the number of entries in the cache.
func (c *BlockHashCache) Len() int {
	return c.order.Len()
}

// Clear removes all entries from the cache.
func (c *BlockHashCache) Clear() {
	c.items = make(map[uint64]*list.Element, c.capacity)
	c.order.Init()
}