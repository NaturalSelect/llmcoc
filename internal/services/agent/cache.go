package agent

import (
	"container/list"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/philippgille/chromem-go"
)

// NOTE: CacheEntry 表示单条缓存快照，供管理接口安全返回。
type CacheEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Size  int64  `json:"size"`
}

// LawyerCache is an LRU cache for final lawyer rulings.
// It stores finalized answer text keyed by situation.
type LawyerCache struct {
	mu       sync.RWMutex
	cache    map[string]*list.Element
	list     *list.List
	maxBytes int64 // Maximum capacity in bytes
	curBytes int64 // Current size in bytes

	// NOTE: index 只收 vecModel == indexModel 的条目，文档 ID 就是条目的标签串；
	// 两者要么同时为空（未建索引），要么同时有值。所有改动都在持有 mu 时同步做。
	index      *chromem.Collection
	indexModel string

	// Hit statistics (atomic)
	fullHits    atomic.Int64 // Go-level cache hit, LLM not invoked
	partialHits atomic.Int64 // LLM invoked, search_cache matched, no rulebook search needed
	misses      atomic.Int64 // LLM invoked and had to search the rulebook
}

// cacheNode is the internal node stored in the list.
type cacheNode struct {
	key   string
	value string
	size  int64 // Size in bytes，含向量

	// NOTE: vector 已 L2 归一化；vecModel 是产生它的嵌入模型身份，换模型后与当前模型不符的向量不参与检索。
	vector   []float32
	vecModel string
}

// LawyerCacheHashes binds persisted rulings to the exact reference documents used to produce them.
type LawyerCacheHashes struct {
	RulebookHash    string `json:"rulebook_hash"`
	SpellbookHash   string `json:"spellbook_hash"`
	MonsterbookHash string `json:"monsterbook_hash"`
}

type persistentLawyerCache struct {
	Hashes  LawyerCacheHashes  `json:"hashes"`
	SavedAt time.Time          `json:"saved_at"`
	Entries []persistentRuling `json:"entries"`
}

type persistentRuling struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// NOTE: 向量存小端 float32 原始字节(JSON 里是 base64)；缺字段表示该条目还没有向量，旧文件可直接加载。
	Vector   []byte `json:"vector,omitempty"`
	VecModel string `json:"vec_model,omitempty"`
}

func (h LawyerCacheHashes) complete() bool {
	return h.RulebookHash != "" && h.SpellbookHash != "" && h.MonsterbookHash != ""
}

func (h LawyerCacheHashes) matches(other LawyerCacheHashes) bool {
	return h.complete() && other.complete() &&
		h.RulebookHash == other.RulebookHash &&
		h.SpellbookHash == other.SpellbookHash &&
		h.MonsterbookHash == other.MonsterbookHash
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
	entry, ok := lc.GetEntry(key)
	if !ok {
		return "", false
	}
	return entry.Value, true
}

// NOTE: GetEntry 返回缓存条目的完整只读快照，并刷新 LRU 顺序。
func (lc *LawyerCache) GetEntry(key string) (CacheEntry, bool) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	elem, exists := lc.cache[key]
	if !exists {
		return CacheEntry{}, false
	}
	lc.list.MoveToFront(elem)
	node := elem.Value.(*cacheNode)
	return CacheEntry{Key: node.key, Value: node.value, Size: node.size}, true
}

// Set stores a value in the cache with the given key.
func (lc *LawyerCache) Set(key, value string) {
	if key == "" {
		return
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.setLocked(key, value, nil, "")
}

// NOTE: SetWithVector 写入条目并带上其向量；vector 为空表示嵌入失败或未启用，先存无向量条目，之后由补算填上。
func (lc *LawyerCache) SetWithVector(key, value string, vector []float32, vecModel string) {
	if key == "" {
		return
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.setLocked(key, value, vector, vecModel)
}

func (lc *LawyerCache) setLocked(key, value string, vector []float32, vecModel string) {
	if len(vector) == 0 {
		vector, vecModel = nil, ""
	}
	size := int64(len(key) + len(value) + 4*len(vector))

	// If key already exists, update it
	if elem, exists := lc.cache[key]; exists {
		node := elem.Value.(*cacheNode)
		lc.curBytes += size - node.size
		node.value = value
		node.size = size
		node.vector = vector
		node.vecModel = vecModel
		lc.list.MoveToFront(elem)
		lc.reindexLocked(node)
		return
	}

	// Evict entries until there's space for the new entry
	for lc.curBytes+size > lc.maxBytes && lc.list.Len() > 0 {
		lc.evictLocked()
	}

	// Add new entry
	node := &cacheNode{
		key:      key,
		value:    value,
		size:     size,
		vector:   vector,
		vecModel: vecModel,
	}
	elem := lc.list.PushFront(node)
	lc.cache[key] = elem
	lc.curBytes += size
	lc.reindexLocked(node)
}

// NOTE: AttachVector 只在条目正文仍等于 expectValue 时挂上向量，避免补算的旧结果覆盖并发写入的新内容。
func (lc *LawyerCache) AttachVector(key, expectValue string, vector []float32, vecModel string) bool {
	if len(vector) == 0 || vecModel == "" {
		return false
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()

	elem, exists := lc.cache[key]
	if !exists {
		return false
	}
	node := elem.Value.(*cacheNode)
	if node.value != expectValue {
		return false
	}
	lc.curBytes += int64(4 * (len(vector) - len(node.vector)))
	node.size += int64(4 * (len(vector) - len(node.vector)))
	node.vector = vector
	node.vecModel = vecModel
	lc.reindexLocked(node)
	return true
}

// MissingVectors 列出没有 model 对应向量的条目，供后台补算使用。
func (lc *LawyerCache) MissingVectors(model string) []CacheEntry {
	lc.mu.RLock()
	defer lc.mu.RUnlock()

	var missing []CacheEntry
	for elem := lc.list.Front(); elem != nil; elem = elem.Next() {
		node := elem.Value.(*cacheNode)
		if node.vecModel != model || len(node.vector) == 0 {
			missing = append(missing, CacheEntry{Key: node.key, Value: node.value, Size: node.size})
		}
	}
	return missing
}

// reindexLocked 让索引与节点的当前向量保持一致：先删旧文档，再按需加入。
func (lc *LawyerCache) reindexLocked(node *cacheNode) {
	if lc.index == nil {
		return
	}
	_ = lc.index.Delete(context.Background(), nil, nil, node.key)
	if node.vecModel == lc.indexModel && len(node.vector) > 0 {
		// NOTE: 文档 ID 和向量都非空时 AddDocument 不会失败，也不会调用嵌入函数。
		_ = lc.index.AddDocument(context.Background(), chromem.Document{ID: node.key, Embedding: node.vector})
	}
}

// rebuildIndexLocked 按 model 重建索引，只收 vecModel == model 的条目。
func (lc *LawyerCache) rebuildIndexLocked(model string) {
	// NOTE: 必须传桩嵌入函数：传 nil 会回落到 chromem 默认的 OpenAI 函数并发真实请求。我们只用 QueryEmbedding。
	stub := func(context.Context, string) ([]float32, error) {
		return nil, errors.New("lawyer cache index does not embed text")
	}
	col, err := chromem.NewDB().CreateCollection("lawyer_cache", nil, stub)
	if err != nil {
		lc.index, lc.indexModel = nil, ""
		return
	}
	lc.index, lc.indexModel = col, model
	for elem := lc.list.Front(); elem != nil; elem = elem.Next() {
		node := elem.Value.(*cacheNode)
		if node.vecModel == model && len(node.vector) > 0 {
			_ = col.AddDocument(context.Background(), chromem.Document{ID: node.key, Embedding: node.vector})
		}
	}
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
	lc.dropFromIndexLocked(node.key)
}

func (lc *LawyerCache) dropFromIndexLocked(key string) {
	if lc.index != nil {
		_ = lc.index.Delete(context.Background(), nil, nil, key)
	}
}

// Delete removes a single entry by key. Returns true if the key existed.
func (lc *LawyerCache) Delete(key string) bool {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	elem, exists := lc.cache[key]
	if !exists {
		return false
	}
	lc.list.Remove(elem)
	node := elem.Value.(*cacheNode)
	delete(lc.cache, node.key)
	lc.curBytes -= node.size
	lc.dropFromIndexLocked(node.key)
	return true
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
	lc.index, lc.indexModel = nil, ""
}

// LoadFromFile replaces the cache with entries from path when all document hashes match.
func (lc *LawyerCache) LoadFromFile(path string, hashes LawyerCacheHashes) (bool, error) {
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
	if !payload.Hashes.matches(hashes) {
		return false, nil
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.clearLocked()
	for i := len(payload.Entries) - 1; i >= 0; i-- {
		entry := payload.Entries[i]
		lc.setLocked(entry.Key, entry.Value, decodeVector(entry.Vector), entry.VecModel)
	}
	return true, nil
}

// encodeVector 把向量编码成小端 float32 字节。
func encodeVector(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[4*i:], math.Float32bits(f))
	}
	return buf
}

// decodeVector 是 encodeVector 的逆过程；长度不是 4 的倍数或含 NaN/Inf 时丢弃，该条目按无向量处理。
func decodeVector(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		f := math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return nil
		}
		v[i] = f
	}
	return v
}

// SaveToFile writes the cache to path together with the current document hashes.
func (lc *LawyerCache) SaveToFile(path string, hashes LawyerCacheHashes) error {
	lc.mu.RLock()
	entries := make([]persistentRuling, 0, len(lc.cache))
	for elem := lc.list.Front(); elem != nil; elem = elem.Next() {
		node := elem.Value.(*cacheNode)
		entries = append(entries, persistentRuling{
			Key:      node.key,
			Value:    node.value,
			Vector:   encodeVector(node.vector),
			VecModel: node.vecModel,
		})
	}
	lc.mu.RUnlock()

	payload := persistentLawyerCache{
		Hashes:  hashes,
		SavedAt: time.Now(),
		Entries: entries,
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

// SetStats sets all hit/miss counters to the given values.
func (lc *LawyerCache) SetStats(full, partial, miss int64) {
	lc.fullHits.Store(full)
	lc.partialHits.Store(partial)
	lc.misses.Store(miss)
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

// ListKeysPaginated returns a sorted page of cache keys and the total count.
// page and pageSize are 1-based; returns empty slice if out of range.
func (lc *LawyerCache) ListKeysPaginated(page, pageSize int) (keys []string, total int) {
	lc.mu.RLock()
	defer lc.mu.RUnlock()

	total = len(lc.cache)
	if page < 1 || pageSize < 1 {
		return
	}
	all := make([]string, 0, total)
	for k := range lc.cache {
		all = append(all, k)
	}
	sortStrings(all)
	start := (page - 1) * pageSize
	if start >= total {
		return
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	keys = all[start:end]
	return
}

// sortStrings sorts a string slice in ascending order via insertion sort.
func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// CacheMatch is one result returned by Search.
type CacheMatch struct {
	Key        string
	Ruling     string
	Score      int     // 标签匹配：命中的查询标签数
	Similarity float32 // 向量匹配：余弦相似度；标签匹配时为 0
}

// SearchVector 在索引里按余弦相似度取前 topK 条，丢弃低于 minSim 的结果，按相似度降序返回。
// model 与当前索引的模型不同时先按 model 重建索引；出错(如向量维度不一致)由调用方退回标签匹配。
func (lc *LawyerCache) SearchVector(model string, query []float32, topK int, minSim float32) ([]CacheMatch, error) {
	if model == "" || len(query) == 0 || topK <= 0 {
		return nil, nil
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.index == nil || lc.indexModel != model {
		lc.rebuildIndexLocked(model)
	}
	if lc.index == nil {
		return nil, nil
	}
	// NOTE: chromem 在 nResults 大于文档数时直接报错，必须先截断。
	n := lc.index.Count()
	if n == 0 {
		return nil, nil
	}
	if topK > n {
		topK = n
	}
	results, err := lc.index.QueryEmbedding(context.Background(), query, topK, nil, nil)
	if err != nil {
		return nil, err
	}

	var matches []CacheMatch
	for _, r := range results {
		if r.Similarity < minSim {
			continue
		}
		elem, ok := lc.cache[r.ID]
		if !ok {
			continue
		}
		node := elem.Value.(*cacheNode)
		matches = append(matches, CacheMatch{Key: node.key, Ruling: node.value, Similarity: r.Similarity})
	}
	slices.SortStableFunc(matches, func(a, b CacheMatch) int {
		switch {
		case a.Similarity > b.Similarity:
			return -1
		case a.Similarity < b.Similarity:
			return 1
		}
		return strings.Compare(a.Key, b.Key)
	})
	return matches, nil
}

// SearchTagsUnvectorized 只在没有 model 对应向量的条目里做标签子串匹配，作为向量检索的兜底。
func (lc *LawyerCache) SearchTagsUnvectorized(model, query string, topK int) []CacheMatch {
	return lc.searchTags(query, topK, func(n *cacheNode) bool {
		return n.vecModel != model || len(n.vector) == 0
	})
}

// Search returns up to topK cached entries whose keys contain the most query
// tokens (case-insensitive substring match). Entries are ordered by score desc.
func (lc *LawyerCache) Search(query string, topK int) []CacheMatch {
	return lc.searchTags(query, topK, nil)
}

// searchTags 是标签子串匹配的实现；include 非空时只考虑它返回 true 的条目。
func (lc *LawyerCache) searchTags(query string, topK int, include func(*cacheNode) bool) []CacheMatch {
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
		if include != nil && !include(elem.Value.(*cacheNode)) {
			continue
		}
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
