package llm

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/llmcoc/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// initLLMTestDB sets up an isolated in-memory SQLite DB for llm package tests.
func initLLMTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&models.LLMLatencyStat{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		_ = sqlDB.Close()
	})
}

func TestRoleFromCacheKey(t *testing.T) {
	cases := map[string]string{
		"sess:writer":          "writer",
		"sess:npc:张三":          "npc",
		"sess:lawyer:q1":       "lawyer",
		"nosession:evaluator":  "evaluator",
		"admin:ping":           "ping",
		"":                     "unknown",
		"no-colon-here":        "unknown",
		"sess:":                "unknown",
	}
	for key, want := range cases {
		if got := roleFromCacheKey(key); got != want {
			t.Errorf("roleFromCacheKey(%q) = %q, want %q", key, got, want)
		}
	}
}

func resetStatsForTest() {
	statsMu.Lock()
	statsEntries = make(map[statKey]*statEntry)
	statsMu.Unlock()
}

func TestRecordLatencyAveragesBySumAndCount(t *testing.T) {
	resetStatsForTest()
	recordLatency("writer", "gpt-4o", "chat", 100*time.Millisecond, nil)
	recordLatency("writer", "gpt-4o", "chat", 300*time.Millisecond, nil)
	recordLatency("writer", "gpt-4o", "chat", 200*time.Millisecond, errors.New("boom"))

	got := Stats()
	if got.Overall.Count != 3 {
		t.Fatalf("overall count = %d, want 3", got.Overall.Count)
	}
	if got.Overall.AvgMs != 200 {
		t.Fatalf("overall avg = %v, want 200", got.Overall.AvgMs)
	}
	if got.Overall.MaxMs != 300 {
		t.Fatalf("overall max = %d, want 300", got.Overall.MaxMs)
	}
	if got.Overall.ErrCount != 1 {
		t.Fatalf("overall err count = %d, want 1", got.Overall.ErrCount)
	}
	if len(got.ByRole) != 1 || got.ByRole[0].Key != "writer" || got.ByRole[0].Count != 3 {
		t.Fatalf("by role = %+v, want single writer entry with count 3", got.ByRole)
	}
	if len(got.ByModel) != 1 || got.ByModel[0].Key != "gpt-4o" {
		t.Fatalf("by model = %+v, want single gpt-4o entry", got.ByModel)
	}
}

func TestRecordLatencyConcurrentWrites(t *testing.T) {
	resetStatsForTest()
	const n = 200
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			recordLatency("director", "gpt-4o", "chat", time.Duration(i)*time.Millisecond, nil)
		}(i)
	}
	wg.Wait()

	got := Stats()
	if got.Overall.Count != n {
		t.Fatalf("overall count = %d, want %d", got.Overall.Count, n)
	}
}

func TestRecordLatencyCardinalityCapFoldsToOther(t *testing.T) {
	resetStatsForTest()
	for i := 0; i < maxStatKeys; i++ {
		recordLatency("role", string(rune('a'+i%26))+string(rune(i)), "chat", time.Millisecond, nil)
	}
	// 已达上限，新的 key 组合应折叠进 role="other"。
	recordLatency("brand-new-role", "brand-new-model", "chat", 5*time.Millisecond, nil)

	got := Stats()
	found := false
	for _, line := range got.ByRole {
		if line.Key == "other" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an 'other' role entry after exceeding cardinality cap, got %+v", got.ByRole)
	}
}

func TestResetStatsClearsMemoryAndDB(t *testing.T) {
	initLLMTestDB(t)
	resetStatsForTest()
	recordLatency("writer", "gpt-4o", "chat", 10*time.Millisecond, nil)
	persistStats()

	var count int64
	models.DB.Model(&models.LLMLatencyStat{}).Count(&count)
	if count == 0 {
		t.Fatal("expected at least one persisted row before reset")
	}

	ResetStats()

	if got := Stats(); got.Overall.Count != 0 {
		t.Fatalf("overall count after reset = %d, want 0", got.Overall.Count)
	}
	models.DB.Model(&models.LLMLatencyStat{}).Count(&count)
	if count != 0 {
		t.Fatalf("db rows after reset = %d, want 0", count)
	}
}

func TestPersistAndLoadStatsRoundTrip(t *testing.T) {
	initLLMTestDB(t)
	resetStatsForTest()
	recordLatency("director", "gpt-4o", "chat", 100*time.Millisecond, nil)
	recordLatency("director", "gpt-4o", "chat", 200*time.Millisecond, errors.New("boom"))
	persistStats()

	// 模拟进程重启：清空内存，重新从库里加载。
	resetStatsForTest()
	if got := Stats(); got.Overall.Count != 0 {
		t.Fatalf("expected empty stats before LoadStats, got count=%d", got.Overall.Count)
	}

	LoadStats()

	got := Stats()
	if got.Overall.Count != 2 {
		t.Fatalf("overall count after LoadStats = %d, want 2", got.Overall.Count)
	}
	if got.Overall.AvgMs != 150 {
		t.Fatalf("overall avg after LoadStats = %v, want 150", got.Overall.AvgMs)
	}
	if got.Overall.ErrCount != 1 {
		t.Fatalf("overall err count after LoadStats = %d, want 1", got.Overall.ErrCount)
	}

	// 再次 persistStats 应该是 upsert 而不是插入新行。
	recordLatency("director", "gpt-4o", "chat", 300*time.Millisecond, nil)
	persistStats()
	var count int64
	models.DB.Model(&models.LLMLatencyStat{}).Count(&count)
	if count != 1 {
		t.Fatalf("db row count after second persist = %d, want 1 (upsert not insert)", count)
	}
}
