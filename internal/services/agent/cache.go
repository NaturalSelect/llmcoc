package agent

import (
	"container/list"
	"sync"
)

// CacheEntry represents a single cached item.
type CacheEntry struct {
	Key   string
	Value string
}

// LawyerCache is an LRU cache for final lawyer rulings.
// It stores finalized answer text keyed by situation.
type LawyerCache struct {
	mu       sync.RWMutex
	cache    map[string]*list.Element
	list     *list.List
	maxBytes int64 // Maximum capacity in bytes
	curBytes int64 // Current size in bytes
}

// cacheNode is the internal node stored in the list.
type cacheNode struct {
	key   string
	value string
	size  int64 // Size in bytes
}

// NewLawyerCache creates a new LRU cache with a specified capacity in bytes.
// maxBytes: maximum capacity (recommend: 1GB = 1073741824 bytes)
func NewLawyerCache(maxBytes int64) *LawyerCache {
	return &LawyerCache{
		cache:    make(map[string]*list.Element),
		list:     list.New(),
		maxBytes: maxBytes,
		curBytes: 0,
	}
}

// Get retrieves a cached value by key.
func (lc *LawyerCache) Get(key string) (string, bool) {
	lc.mu.RLock()
	elem, exists := lc.cache[key]
	lc.mu.RUnlock()

	if !exists {
		return "", false
	}

	// Move to front (most recently used)
	lc.mu.Lock()
	lc.list.MoveToFront(elem)
	node := elem.Value.(*cacheNode)
	value := node.value
	lc.mu.Unlock()

	return value, true
}

// Set stores a value in the cache with the given key.
func (lc *LawyerCache) Set(key, value string) {
	if key == "" {
		return
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()

	size := int64(len(key) + len(value))

	// If key already exists, update it
	if elem, exists := lc.cache[key]; exists {
		oldNode := elem.Value.(*cacheNode)
		lc.curBytes -= oldNode.size
		oldNode.value = value
		oldNode.size = size
		lc.curBytes += size
		lc.list.MoveToFront(elem)
		return
	}

	// Evict entries until there's space for the new entry
	for lc.curBytes+size > lc.maxBytes && lc.list.Len() > 0 {
		lc.evictLocked()
	}

	// Add new entry
	node := &cacheNode{
		key:   key,
		value: value,
		size:  size,
	}
	elem := lc.list.PushFront(node)
	lc.cache[key] = elem
	lc.curBytes += size
}

// evictLocked removes the least recently used entry (tail of list).
// Must be called while holding the mutex.
func (lc *LawyerCache) evictLocked() {
	elem := lc.list.Back()
	if elem == nil {
		return
	}

	lc.list.Remove(elem)
	node := elem.Value.(*cacheNode)
	delete(lc.cache, node.key)
	lc.curBytes -= node.size
}

// Clear removes all entries from the cache.
func (lc *LawyerCache) Clear() {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	lc.cache = make(map[string]*list.Element)
	lc.list = list.New()
	lc.curBytes = 0
}

// Stats returns cache statistics: (entries_count, used_bytes, max_bytes).
func (lc *LawyerCache) Stats() (entries int, usedBytes int64, maxBytes int64) {
	lc.mu.RLock()
	defer lc.mu.RUnlock()

	return len(lc.cache), lc.curBytes, lc.maxBytes
}

// ListKeys returns all cache keys in the cache.
func (lc *LawyerCache) ListKeys() []string {
	lc.mu.RLock()
	defer lc.mu.RUnlock()

	keys := make([]string, 0, len(lc.cache))
	for k := range lc.cache {
		keys = append(keys, k)
	}
	return keys
}
