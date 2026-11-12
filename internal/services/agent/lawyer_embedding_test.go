package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const fakeEmbedModel = "test:fake"

// fakeEmbedder 按关键字把文本映射成固定的二维单位向量：含"手枪"的指向 x，含"侦查"的指向 y，
// 其余指向 (0.6,0.8) 的斜向。余弦相似度就是夹角余弦，便于手算。
type fakeEmbedder struct {
	calls atomic.Int32
	fail  atomic.Bool
}

func (f *fakeEmbedder) embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls.Add(1)
	if f.fail.Load() {
		return nil, errors.New("embedding service down")
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		switch {
		case strings.Contains(t, "手枪"):
			out[i] = []float32{1, 0}
		case strings.Contains(t, "侦查"):
			out[i] = []float32{0, 1}
		default:
			out[i] = []float32{0.6, 0.8}
		}
	}
	return out, nil
}

// useFakeEmbedder 替换 loader 并清空全局缓存，测试结束后还原。
func useFakeEmbedder(t *testing.T) *fakeEmbedder {
	t.Helper()
	f := &fakeEmbedder{}
	prev := lawyerEmbedderLoader
	lawyerEmbedderLoader = func() (*lawyerEmbedder, bool) {
		return &lawyerEmbedder{embed: f.embed, model: fakeEmbedModel}, true
	}
	lawyerCache.Clear()
	t.Cleanup(func() {
		waitLawyerBackfill()
		lawyerEmbedderLoader = prev
		lawyerCache.Clear()
	})
	return f
}

func searchCacheResult(t *testing.T, prov *sequentialFakeProvider, round int) string {
	t.Helper()
	msgs := prov.recordedMessages[round]
	return msgs[len(msgs)-1].Content
}

func TestRunLawyerSearchCacheUsesVectorSimilarity(t *testing.T) {
	initSiteSettingTestDB(t)
	useFakeEmbedder(t)

	lawyerCache.SetWithVector("#武器 #左轮 #伤害", "左轮的伤害", []float32{1, 0}, fakeEmbedModel)
	lawyerCache.SetWithVector("#技能 #侦查 #成功等级", "侦查的成功等级", []float32{0, 1}, fakeEmbedModel)

	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#手枪 #伤害"}`)}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c2", toolNameLawyerResponse, responseArgsJSON(t, "裁定"))}},
		},
	}
	runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	got := searchCacheResult(t, prov, 1)
	if !strings.Contains(got, "相似度 1.00") || !strings.Contains(got, "标签：#武器 #左轮 #伤害") {
		t.Errorf("vector hit must report similarity and key, got %q", got)
	}
	// "#左轮" 与查询 "#手枪" 没有任何公共子串：只有向量检索才能命中它。
	if strings.Contains(got, "#侦查") {
		t.Errorf("orthogonal entry (similarity 0) must be filtered by the threshold, got %q", got)
	}
}

func TestRunLawyerSearchCacheFallsBackToTagsWhenEmbeddingFails(t *testing.T) {
	initSiteSettingTestDB(t)
	f := useFakeEmbedder(t)
	f.fail.Store(true)

	lawyerCache.Set("#相关度专用标签 #伤害 #武器", "回退用例的裁定")

	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#相关度专用标签 #伤害 #绝不存在xyz"}`)}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c2", toolNameLawyerResponse, responseArgsJSON(t, "裁定"))}},
		},
	}
	runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	got := searchCacheResult(t, prov, 1)
	if !strings.Contains(got, "匹配 2/3 标签") {
		t.Errorf("embedding failure must fall back to tag matching, got %q", got)
	}
}

func TestRunLawyerSearchCacheAddsTagMatchesForUnvectorizedEntries(t *testing.T) {
	initSiteSettingTestDB(t)
	useFakeEmbedder(t)
	// NOTE: 补算在检索结果算完之后才异步触发，所以本次检索时无向量条目一定还没有向量。
	lawyerCache.SetWithVector("#武器 #手枪 #伤害", "有向量的条目", []float32{1, 0}, fakeEmbedModel)
	lawyerCache.Set("#武器 #步枪 #伤害", "没有向量的条目")

	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#手枪 #步枪"}`)}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c2", toolNameLawyerResponse, responseArgsJSON(t, "裁定"))}},
		},
	}
	runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	got := searchCacheResult(t, prov, 1)
	if !strings.Contains(got, "相似度") || !strings.Contains(got, "匹配 1/2 标签") {
		t.Errorf("want a vector hit plus a tag-matched unvectorized entry, got %q", got)
	}
}

func TestRunLawyerSaveCacheStoresVector(t *testing.T) {
	initSiteSettingTestDB(t)
	useFakeEmbedder(t)

	const key = "#武器 #手枪 #伤害"
	const ruling = "手枪伤害1D10"
	saveArgs := `{"cache_key":"` + key + `","ruling":"` + ruling + `"}`
	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#手枪"}`)}},
			{ToolCalls: []llm.ToolCall{
				fakeToolCall("c2", toolNameLawyerResponse, responseArgsJSON(t, ruling)),
				fakeToolCall("c3", toolNameSaveCache, saveArgs),
			}},
		},
	}
	runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")
	waitLawyerBackfill()

	got, err := lawyerCache.SearchVector(fakeEmbedModel, []float32{1, 0}, 10, 0.9)
	if err != nil || len(got) != 1 || got[0].Key != key {
		t.Fatalf("save_cache must store a vector under the embedder's model, got %v err=%v", got, err)
	}
}

func TestRunLawyerSaveCacheWithoutVectorWhenEmbeddingFails(t *testing.T) {
	initSiteSettingTestDB(t)
	f := useFakeEmbedder(t)
	f.fail.Store(true)

	saveArgs := `{"cache_key":"#武器 #手枪 #伤害","ruling":"手枪伤害1D10"}`
	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#手枪"}`)}},
			{ToolCalls: []llm.ToolCall{
				fakeToolCall("c2", toolNameLawyerResponse, responseArgsJSON(t, "手枪伤害1D10")),
				fakeToolCall("c3", toolNameSaveCache, saveArgs),
			}},
		},
	}
	runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	if v, ok := lawyerCache.Get("#武器 #手枪 #伤害"); !ok || v != "手枪伤害1D10" {
		t.Fatalf("entry must still be saved when embedding fails, got %q %v", v, ok)
	}
	if missing := lawyerCache.MissingVectors(fakeEmbedModel); len(missing) != 1 {
		t.Fatalf("entry saved without a vector must be listed for backfill, got %v", missing)
	}
}

func TestLawyerBackfillFillsMissingVectors(t *testing.T) {
	initSiteSettingTestDB(t)
	f := useFakeEmbedder(t)

	lawyerCache.Set("#武器 #手枪 #伤害", "手枪伤害")
	lawyerCache.SetWithVector("#技能 #侦查", "旧模型的向量", []float32{0, 1}, "old:model")

	ensureLawyerCacheBackfill()
	waitLawyerBackfill()

	if missing := lawyerCache.MissingVectors(fakeEmbedModel); len(missing) != 0 {
		t.Fatalf("backfill must fill entries lacking a vector for the current model, still missing %v", missing)
	}
	got, err := lawyerCache.SearchVector(fakeEmbedModel, []float32{1, 0}, 10, 0.9)
	if err != nil || len(got) != 1 || got[0].Key != "#武器 #手枪 #伤害" {
		t.Fatalf("backfilled vector must be searchable, got %v err=%v", got, err)
	}
	if f.calls.Load() != 1 {
		t.Errorf("both entries fit in one batch, embed calls = %d, want 1", f.calls.Load())
	}
}

func TestLawyerBackfillStopsOnFailureAndRetriesLater(t *testing.T) {
	initSiteSettingTestDB(t)
	f := useFakeEmbedder(t)
	f.fail.Store(true)

	lawyerCache.Set("#武器 #手枪 #伤害", "手枪伤害")
	ensureLawyerCacheBackfill()
	waitLawyerBackfill()
	if missing := lawyerCache.MissingVectors(fakeEmbedModel); len(missing) != 1 {
		t.Fatalf("failed backfill must leave the entry without a vector, got %v", missing)
	}

	f.fail.Store(false)
	ensureLawyerCacheBackfill()
	waitLawyerBackfill()
	if missing := lawyerCache.MissingVectors(fakeEmbedModel); len(missing) != 0 {
		t.Fatalf("a later backfill must fill it, still missing %v", missing)
	}
}

func TestLawyerBackfillDoesNotOverwriteConcurrentlyChangedEntry(t *testing.T) {
	initSiteSettingTestDB(t)
	useFakeEmbedder(t)

	lawyerCache.Set("k", "old")
	missing := lawyerCache.MissingVectors(fakeEmbedModel)
	lawyerCache.Set("k", "new") // 补算期间被改写
	if lawyerCache.AttachVector(missing[0].Key, missing[0].Value, []float32{1, 0}, fakeEmbedModel) {
		t.Fatal("stale backfill result must not attach to a rewritten entry")
	}
}

func TestLawyerMinSimilarityFromSiteSetting(t *testing.T) {
	initSiteSettingTestDB(t)

	const def float32 = 0.3
	if got := lawyerMinSimilarity(); got != def {
		t.Errorf("unset setting: got %v, want default %v", got, def)
	}
	cases := []struct {
		value string
		want  float32
	}{
		{"0.55", 0.55},
		{"-0.2", -0.2},
		{" 0.7 ", 0.7},
		{"abc", def},
		{"1.5", def},
		{"-1.5", def},
		{"", def},
	}
	for _, tc := range cases {
		models.DB.Where("key = ?", lawyerCacheMinSimilarityKey).Delete(&models.SiteSetting{})
		if err := models.DB.Create(&models.SiteSetting{Key: lawyerCacheMinSimilarityKey, Value: tc.value}).Error; err != nil {
			t.Fatalf("seed setting %q: %v", tc.value, err)
		}
		if got := lawyerMinSimilarity(); got != tc.want {
			t.Errorf("setting %q: got %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestLawyerEmbedTextTruncatesByRune(t *testing.T) {
	long := strings.Repeat("规", lawyerEmbedMaxRunes+500)
	got := lawyerEmbedText("#键", long)
	if n := len([]rune(got)); n != lawyerEmbedMaxRunes {
		t.Errorf("embed text runes = %d, want %d", n, lawyerEmbedMaxRunes)
	}
	if got := lawyerEmbedText("#键", "短"); got != "#键\n短" {
		t.Errorf("short text must not be altered, got %q", got)
	}
}

func TestLoadLawyerEmbedderUnavailableWithoutEnabledRole(t *testing.T) {
	initSiteSettingTestDB(t)
	if e, ok := loadLawyerEmbedder(); ok || e != nil {
		t.Fatalf("no embedding role in the database must mean unavailable, got %v %v", e, ok)
	}
}
