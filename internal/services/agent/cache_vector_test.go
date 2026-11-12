package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// NOTE: 测试用的向量都是二维单位向量，余弦相似度就是夹角余弦，便于手算。
var (
	vecX  = []float32{1, 0}
	vecY  = []float32{0, 1}
	vecXY = []float32{0.8, 0.6}
)

func matchKeys(ms []CacheMatch) []string {
	keys := make([]string, 0, len(ms))
	for _, m := range ms {
		keys = append(keys, m.Key)
	}
	return keys
}

func TestLawyerCacheSearchVectorOrdersBySimilarityAndFiltersByThreshold(t *testing.T) {
	c := NewLawyerCache(1 << 20)
	c.SetWithVector("#武器 #手枪", "手枪伤害", vecX, "m1")
	c.SetWithVector("#武器 #霰弹枪", "霰弹枪伤害", vecXY, "m1")
	c.SetWithVector("#技能 #侦查", "侦查成功等级", vecY, "m1")

	got, err := c.SearchVector("m1", vecX, 10, 0.5)
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}
	keys := matchKeys(got)
	if len(keys) != 2 || keys[0] != "#武器 #手枪" || keys[1] != "#武器 #霰弹枪" {
		t.Fatalf("want [手枪 霰弹枪] by similarity, got %v", keys)
	}
	if got[0].Similarity < 0.99 || got[1].Similarity < 0.79 || got[1].Similarity > 0.81 {
		t.Fatalf("unexpected similarities: %v, %v", got[0].Similarity, got[1].Similarity)
	}
}

func TestLawyerCacheSearchVectorTopKLargerThanEntriesDoesNotFail(t *testing.T) {
	c := NewLawyerCache(1 << 20)
	if got, err := c.SearchVector("m1", vecX, 10, 0); err != nil || got != nil {
		t.Fatalf("empty cache: want nil,nil got %v,%v", got, err)
	}
	c.SetWithVector("a", "A", vecX, "m1")
	got, err := c.SearchVector("m1", vecX, 10, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("topK 10 over 1 entry: want 1 result, got %v,%v", got, err)
	}
}

func TestLawyerCacheVectorSearchIgnoresOtherModelsButTagSearchCoversThem(t *testing.T) {
	c := NewLawyerCache(1 << 20)
	c.SetWithVector("#武器 #手枪", "新模型向量", vecX, "m2")
	c.SetWithVector("#武器 #左轮手枪", "旧模型向量", vecX, "m1")
	c.Set("#武器 #步枪", "没有向量")

	got, err := c.SearchVector("m2", vecX, 10, 0)
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}
	if keys := matchKeys(got); len(keys) != 1 || keys[0] != "#武器 #手枪" {
		t.Fatalf("only m2 entries may appear in vector results, got %v", keys)
	}

	tags := c.SearchTagsUnvectorized("m2", "#武器", 10)
	keys := matchKeys(tags)
	if len(keys) != 2 {
		t.Fatalf("tag fallback must cover entries without an m2 vector, got %v", keys)
	}
	for _, k := range keys {
		if k == "#武器 #手枪" {
			t.Fatalf("entry with m2 vector must not appear in the tag fallback: %v", keys)
		}
	}
}

func TestLawyerCacheIndexFollowsOverwriteDeleteEvictAndClear(t *testing.T) {
	c := NewLawyerCache(1 << 20)
	c.SetWithVector("k1", "v1", vecX, "m1")
	c.SetWithVector("k2", "v2", vecY, "m1")
	if _, err := c.SearchVector("m1", vecX, 10, 0); err != nil {
		t.Fatal(err)
	}

	c.SetWithVector("k1", "v1b", vecY, "m1")
	got, _ := c.SearchVector("m1", vecX, 10, 0.9)
	if len(got) != 0 {
		t.Fatalf("overwritten vector must replace the old one, got %v", matchKeys(got))
	}

	if !c.Delete("k2") {
		t.Fatal("Delete k2 should report true")
	}
	got, _ = c.SearchVector("m1", vecY, 10, 0.9)
	if keys := matchKeys(got); len(keys) != 1 || keys[0] != "k1" {
		t.Fatalf("deleted key must leave the index, got %v", keys)
	}

	c.Set("k1", "plain")
	if got, _ = c.SearchVector("m1", vecY, 10, 0); len(got) != 0 {
		t.Fatalf("overwrite without a vector must drop it from the index, got %v", matchKeys(got))
	}

	c.SetWithVector("k3", "v3", vecX, "m1")
	c.Clear()
	if got, _ = c.SearchVector("m1", vecX, 10, 0); len(got) != 0 {
		t.Fatalf("Clear must empty the index, got %v", matchKeys(got))
	}
}

func TestLawyerCacheEvictionRemovesEntryFromIndex(t *testing.T) {
	// 容量只够放一条带向量的条目：写入第二条时第一条被淘汰。
	size := int64(len("aa") + len("v") + 4*len(vecX))
	c := NewLawyerCache(size)
	c.SetWithVector("aa", "v", vecX, "m1")
	if got, _ := c.SearchVector("m1", vecX, 10, 0); len(got) != 1 {
		t.Fatalf("setup: want 1 result, got %d", len(got))
	}
	c.SetWithVector("bb", "v", vecY, "m1")
	got, _ := c.SearchVector("m1", vecX, 10, 0)
	for _, m := range got {
		if m.Key == "aa" {
			t.Fatalf("evicted key must not be returned from the index: %v", matchKeys(got))
		}
	}
}

func TestLawyerCacheAttachVectorRejectsChangedValue(t *testing.T) {
	c := NewLawyerCache(1 << 20)
	c.Set("k", "old")
	if !c.AttachVector("k", "old", vecX, "m1") {
		t.Fatal("attach on unchanged value should succeed")
	}
	c.Set("k", "new")
	if c.AttachVector("k", "old", vecY, "m1") {
		t.Fatal("attach must be rejected when the value changed meanwhile")
	}
	if c.AttachVector("missing", "x", vecX, "m1") {
		t.Fatal("attach on a missing key must fail")
	}
	if c.AttachVector("k", "new", nil, "m1") {
		t.Fatal("attach with an empty vector must fail")
	}
}

func TestLawyerCacheMissingVectorsListsStaleAndUnvectorized(t *testing.T) {
	c := NewLawyerCache(1 << 20)
	c.SetWithVector("ok", "v", vecX, "m2")
	c.SetWithVector("stale", "v", vecX, "m1")
	c.Set("plain", "v")

	missing := map[string]bool{}
	for _, e := range c.MissingVectors("m2") {
		missing[e.Key] = true
	}
	if len(missing) != 2 || !missing["stale"] || !missing["plain"] || missing["ok"] {
		t.Fatalf("want stale+plain missing, got %v", missing)
	}
}

func TestLawyerCacheVectorAccountsForSize(t *testing.T) {
	c := NewLawyerCache(1 << 20)
	c.Set("k", "v")
	_, before, _ := c.Stats()
	c.AttachVector("k", "v", vecX, "m1")
	_, after, _ := c.Stats()
	if after-before != int64(4*len(vecX)) {
		t.Fatalf("vector bytes must count toward usage: before=%d after=%d", before, after)
	}
	c.Delete("k")
	if _, used, _ := c.Stats(); used != 0 {
		t.Fatalf("usage must return to 0 after delete, got %d", used)
	}
}

func TestLawyerCachePersistenceRoundTripsVectors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lawyer_cache.json")
	hashes := testLawyerCacheHashes()
	c := NewLawyerCache(1 << 20)
	c.SetWithVector("with", "has vector", vecXY, "m1")
	c.Set("without", "no vector")
	if err := c.SaveToFile(path, hashes); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	loaded := NewLawyerCache(1 << 20)
	ok, err := loaded.LoadFromFile(path, hashes)
	if err != nil || !ok {
		t.Fatalf("LoadFromFile: ok=%v err=%v", ok, err)
	}
	got, err := loaded.SearchVector("m1", vecXY, 10, 0.99)
	if err != nil || len(got) != 1 || got[0].Key != "with" {
		t.Fatalf("vector must survive a save/load round trip, got %v err=%v", got, err)
	}
	if missing := loaded.MissingVectors("m1"); len(missing) != 1 || missing[0].Key != "without" {
		t.Fatalf("entry without a vector must stay without one, got %v", missing)
	}
}

func TestLawyerCacheLoadsLegacyFileWithoutVectors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lawyer_cache.json")
	legacy := `{"hashes":{"rulebook_hash":"rule-hash","spellbook_hash":"spell-hash","monsterbook_hash":"monster-hash"},` +
		`"saved_at":"2026-11-10T01:26:46Z","entries":[{"key":"#技能 #侦查","value":"旧格式条目"}]}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewLawyerCache(1 << 20)
	ok, err := c.LoadFromFile(path, testLawyerCacheHashes())
	if err != nil || !ok {
		t.Fatalf("legacy file must load: ok=%v err=%v", ok, err)
	}
	if v, found := c.Get("#技能 #侦查"); !found || v != "旧格式条目" {
		t.Fatalf("legacy entry lost: %q %v", v, found)
	}
	if missing := c.MissingVectors("m1"); len(missing) != 1 {
		t.Fatalf("legacy entry should be listed for backfill, got %v", missing)
	}
}

func TestDecodeVectorRejectsMalformedInput(t *testing.T) {
	if decodeVector([]byte{1, 2, 3}) != nil {
		t.Fatal("length not a multiple of 4 must be rejected")
	}
	nan := encodeVector([]float32{1, float32(zeroF() / zeroF())})
	if decodeVector(nan) != nil {
		t.Fatal("NaN component must be rejected")
	}
	round := decodeVector(encodeVector([]float32{0.25, -0.5}))
	if len(round) != 2 || round[0] != 0.25 || round[1] != -0.5 {
		t.Fatalf("round trip mismatch: %v", round)
	}
}

func zeroF() float64 { return 0 }
