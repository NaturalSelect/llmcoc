package agent

import (
	"container/list"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

	// Hit statistics (atomic)
	fullHits    atomic.Int64 // Go-level cache hit, LLM not invoked
	partialHits atomic.Int64 // LLM invoked, search_cache matched, no rulebook search needed
	misses      atomic.Int64 // LLM invoked and had to search the rulebook
}

// cacheNode is the internal node stored in the list.
type cacheNode struct {
	key   string
	value string
	size  int64 // Size in bytes
}

type persistentLawyerCache struct {
	RulebookHash string             `json:"rulebook_hash"`
	SavedAt      time.Time          `json:"saved_at"`
	Entries      []persistentRuling `json:"entries"`
}

type persistentRuling struct {
	Key   string `json:"key"`
	Value string `json:"value"`
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
	lc.setLocked(key, value)
}

func (lc *LawyerCache) setLocked(key, value string) {
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

	lc.clearLocked()
}

func (lc *LawyerCache) clearLocked() {
	lc.cache = make(map[string]*list.Element)
	lc.list = list.New()
	lc.curBytes = 0
}

// LoadFromFile replaces the cache with entries from path when the rulebook hash matches.
func (lc *LawyerCache) LoadFromFile(path, rulebookHash string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	var payload persistentLawyerCache
	if err := json.Unmarshal(data, &payload); err != nil {
		return false, err
	}
	if payload.RulebookHash != rulebookHash {
		return false, nil
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.clearLocked()
	for i := len(payload.Entries) - 1; i >= 0; i-- {
		entry := payload.Entries[i]
		lc.setLocked(entry.Key, entry.Value)
	}
	return true, nil
}

// SaveToFile writes the cache to path together with the current rulebook hash.
func (lc *LawyerCache) SaveToFile(path, rulebookHash string) error {
	lc.mu.RLock()
	entries := make([]persistentRuling, 0, len(lc.cache))
	for elem := lc.list.Front(); elem != nil; elem = elem.Next() {
		node := elem.Value.(*cacheNode)
		entries = append(entries, persistentRuling{Key: node.key, Value: node.value})
	}
	lc.mu.RUnlock()

	payload := persistentLawyerCache{
		RulebookHash: rulebookHash,
		SavedAt:      time.Now(),
		Entries:      entries,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// Stats returns cache statistics: (entries_count, used_bytes, max_bytes).
func (lc *LawyerCache) Stats() (entries int, usedBytes int64, maxBytes int64) {
	lc.mu.RLock()
	defer lc.mu.RUnlock()

	return len(lc.cache), lc.curBytes, lc.maxBytes
}

// RecordFullHit increments the full-hit counter (Go-level cache returned directly).
func (lc *LawyerCache) RecordFullHit() { lc.fullHits.Add(1) }

// RecordPartialHit increments the partial-hit counter (LLM search_cache matched, no rulebook search).
func (lc *LawyerCache) RecordPartialHit() { lc.partialHits.Add(1) }

// RecordMiss increments the miss counter (LLM had to search the rulebook).
func (lc *LawyerCache) RecordMiss() { lc.misses.Add(1) }

// HitStats returns (fullHits, partialHits, misses).
func (lc *LawyerCache) HitStats() (full, partial, miss int64) {
	return lc.fullHits.Load(), lc.partialHits.Load(), lc.misses.Load()
}

// ResetStats resets all hit/miss counters to zero.
func (lc *LawyerCache) ResetStats() {
	lc.fullHits.Store(0)
	lc.partialHits.Store(0)
	lc.misses.Store(0)
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

// CacheMatch is one result returned by Search.
type CacheMatch struct {
	Key    string
	Ruling string
	Score  int // number of query tokens matched
}

// Search returns up to topK cached entries whose keys contain the most query
// tokens (case-insensitive substring match). Entries are ordered by score desc.
func (lc *LawyerCache) Search(query string, topK int) []CacheMatch {
	if query == "" || topK <= 0 {
		return nil
	}
	tokens := strings.Fields(strings.ToLower(query))
	if len(tokens) == 0 {
		return nil
	}

	lc.mu.RLock()
	defer lc.mu.RUnlock()

	var matches []CacheMatch
	for k, elem := range lc.cache {
		lk := strings.ToLower(k)
		score := 0
		for _, t := range tokens {
			if strings.Contains(lk, t) {
				score++
			}
		}
		if score == 0 {
			continue
		}
		node := elem.Value.(*cacheNode)
		matches = append(matches, CacheMatch{Key: k, Ruling: node.value, Score: score})
	}
	// Sort by score descending, then key ascending for determinism.
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0; j-- {
			a, b := matches[j-1], matches[j]
			if a.Score < b.Score || (a.Score == b.Score && a.Key > b.Key) {
				matches[j-1], matches[j] = matches[j], matches[j-1]
			} else {
				break
			}
		}
	}
	if len(matches) > topK {
		return matches[:topK]
	}
	return matches
}
