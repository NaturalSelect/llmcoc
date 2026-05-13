package agent

import (
	"path/filepath"
	"testing"
)

func TestLawyerCachePersistenceRequiresMatchingRulebookHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lawyer_cache.json")
	cache := NewLawyerCache(1024)
	cache.Set("射击检定", "使用手枪技能检定")
	cache.Set("伤害", "成功命中后掷伤害")

	if err := cache.SaveToFile(path, "hash-a"); err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}

	mismatch := NewLawyerCache(1024)
	loaded, err := mismatch.LoadFromFile(path, "hash-b")
	if err != nil {
		t.Fatalf("LoadFromFile with mismatched hash failed: %v", err)
	}
	if loaded {
		t.Fatal("LoadFromFile loaded cache despite mismatched hash")
	}
	if entries, _, _ := mismatch.Stats(); entries != 0 {
		t.Fatalf("mismatched cache should stay empty, got %d entries", entries)
	}

	matched := NewLawyerCache(1024)
	loaded, err = matched.LoadFromFile(path, "hash-a")
	if err != nil {
		t.Fatalf("LoadFromFile with matching hash failed: %v", err)
	}
	if !loaded {
		t.Fatal("LoadFromFile did not load matching cache")
	}
	if got, ok := matched.Get("射击检定"); !ok || got != "使用手枪技能检定" {
		t.Fatalf("restored cache entry mismatch: ok=%v value=%q", ok, got)
	}
}

func TestLawyerCachePersistencePreservesRecentEntriesUnderCapacity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lawyer_cache.json")
	cache := NewLawyerCache(12)
	cache.Set("a", "1111")
	cache.Set("b", "2222")
	cache.Set("c", "3333")

	if err := cache.SaveToFile(path, "hash"); err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}

	restored := NewLawyerCache(12)
	loaded, err := restored.LoadFromFile(path, "hash")
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}
	if !loaded {
		t.Fatal("LoadFromFile did not load matching cache")
	}
	if _, ok := restored.Get("a"); ok {
		t.Fatal("least recently used entry should have been evicted")
	}
	if got, ok := restored.Get("b"); !ok || got != "2222" {
		t.Fatalf("expected b to be restored, ok=%v value=%q", ok, got)
	}
	if got, ok := restored.Get("c"); !ok || got != "3333" {
		t.Fatalf("expected c to be restored, ok=%v value=%q", ok, got)
	}
}
