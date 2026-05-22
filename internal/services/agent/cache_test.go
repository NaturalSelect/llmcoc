package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func testLawyerCacheHashes() LawyerCacheHashes {
	return LawyerCacheHashes{
		RulebookHash:    "rule-hash",
		SpellbookHash:   "spell-hash",
		MonsterbookHash: "monster-hash",
	}
}

func TestLawyerCachePersistenceRequiresMatchingHashes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lawyer_cache.json")
	hashes := testLawyerCacheHashes()
	cache := NewLawyerCache(1024)
	cache.Set("射击检定", "使用手枪技能检定")
	cache.Set("伤害", "成功命中后掷伤害")

	if err := cache.SaveToFile(path, hashes); err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}

	mismatches := []struct {
		name   string
		hashes LawyerCacheHashes
	}{
		{
			name: "rulebook",
			hashes: LawyerCacheHashes{
				RulebookHash:    "other-rule-hash",
				SpellbookHash:   hashes.SpellbookHash,
				MonsterbookHash: hashes.MonsterbookHash,
			},
		},
		{
			name: "spellbook",
			hashes: LawyerCacheHashes{
				RulebookHash:    hashes.RulebookHash,
				SpellbookHash:   "other-spell-hash",
				MonsterbookHash: hashes.MonsterbookHash,
			},
		},
		{
			name: "monsterbook",
			hashes: LawyerCacheHashes{
				RulebookHash:    hashes.RulebookHash,
				SpellbookHash:   hashes.SpellbookHash,
				MonsterbookHash: "other-monster-hash",
			},
		},
	}
	for _, tc := range mismatches {
		t.Run(tc.name, func(t *testing.T) {
			mismatch := NewLawyerCache(1024)
			loaded, err := mismatch.LoadFromFile(path, tc.hashes)
			if err != nil {
				t.Fatalf("LoadFromFile with mismatched hash failed: %v", err)
			}
			if loaded {
				t.Fatal("LoadFromFile loaded cache despite mismatched hash")
			}
			if entries, _, _ := mismatch.Stats(); entries != 0 {
				t.Fatalf("mismatched cache should stay empty, got %d entries", entries)
			}
		})
	}

	matched := NewLawyerCache(1024)
	loaded, err := matched.LoadFromFile(path, hashes)
	if err != nil {
		t.Fatalf("LoadFromFile with matching hashes failed: %v", err)
	}
	if !loaded {
		t.Fatal("LoadFromFile did not load matching cache")
	}
	if got, ok := matched.Get("射击检定"); !ok || got != "使用手枪技能检定" {
		t.Fatalf("restored cache entry mismatch: ok=%v value=%q", ok, got)
	}
}

func TestLawyerCachePersistenceRejectsMissingHashes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lawyer_cache.json")
	legacyPayload := `{
  "rulebook_hash": "rule-hash",
  "saved_at": "2026-01-01T00:00:00Z",
  "entries": [
    {"key": "旧裁定", "value": "不应加载"}
  ]
}`
	if err := os.WriteFile(path, []byte(legacyPayload), 0644); err != nil {
		t.Fatalf("write legacy cache failed: %v", err)
	}

	cache := NewLawyerCache(1024)
	loaded, err := cache.LoadFromFile(path, testLawyerCacheHashes())
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}
	if loaded {
		t.Fatal("LoadFromFile loaded legacy cache without spell/monster hashes")
	}
	if entries, _, _ := cache.Stats(); entries != 0 {
		t.Fatalf("legacy cache should stay empty, got %d entries", entries)
	}
}

func TestLawyerCachePersistencePreservesRecentEntriesUnderCapacity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lawyer_cache.json")
	hashes := testLawyerCacheHashes()
	cache := NewLawyerCache(12)
	cache.Set("a", "1111")
	cache.Set("b", "2222")
	cache.Set("c", "3333")

	if err := cache.SaveToFile(path, hashes); err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}

	restored := NewLawyerCache(12)
	loaded, err := restored.LoadFromFile(path, hashes)
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
