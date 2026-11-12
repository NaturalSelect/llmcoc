// NOTE: Lawyer 规则缓存的嵌入检索接入：加载嵌入器、查询与保存时算向量、后台补算。
// 嵌入角色未启用、未绑定或 provider 不支持时一律视为不可用，调用方退回标签子串匹配，不影响裁定主流程。
package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const (
	// lawyerCacheMinSimilarityKey 是后台可调的相似度阈值站点设置，低于它的向量命中被丢弃。
	lawyerCacheMinSimilarityKey = "lawyer_cache_min_similarity"

	lawyerEmbedTimeout        = 10 * time.Second
	lawyerBackfillBatchSize   = 32
	lawyerBackfillBatchTimout = 30 * time.Second
	lawyerEmbedMaxRunes       = 2000
)

// lawyerEmbedder 是一次检索/保存所用的嵌入器。model 是向量的身份："<provider_config_id>:<model_name>"，
// 换 provider 或模型后身份变化，旧向量自动不参与检索并被重算。
type lawyerEmbedder struct {
	embed func(ctx context.Context, texts []string) ([][]float32, error)
	model string
}

// lawyerEmbedderLoader 可在测试里替换，避免依赖数据库里的 Provider。
// NOTE: 每次都从库里读 embedding 角色配置，后台改配置后无需额外失效。
var lawyerEmbedderLoader = loadLawyerEmbedder

func loadLawyerEmbedder() (*lawyerEmbedder, bool) {
	h, err := loadSingleAgent(models.AgentRoleEmbedding)
	if err != nil {
		// 角色未启用是常态，只打 debug。
		alog.Debug("lawyer embedding unavailable", "err", err)
		return nil, false
	}
	e, ok := h.provider.(llm.Embedder)
	if !ok {
		alog.Warn("lawyer embedding provider does not support embeddings", "model", h.config.ModelName)
		return nil, false
	}
	var providerID uint
	if h.config.ProviderConfigID != nil {
		providerID = *h.config.ProviderConfigID
	}
	return &lawyerEmbedder{
		embed: e.Embed,
		model: fmt.Sprintf("%d:%s", providerID, h.config.ModelName),
	}, true
}

// lawyerEmbedText 是条目的嵌入文本：标签串加正文，按 rune 截断。
func lawyerEmbedText(key, ruling string) string {
	text := key + "\n" + ruling
	if r := []rune(text); len(r) > lawyerEmbedMaxRunes {
		text = string(r[:lawyerEmbedMaxRunes])
	}
	return text
}

// embedOne 在超时内嵌入单段文本。
func (e *lawyerEmbedder) embedOne(ctx context.Context, text string) ([]float32, error) {
	ctx, cancel := context.WithTimeout(ctx, lawyerEmbedTimeout)
	defer cancel()
	vecs, err := e.embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("embedding returned %d vectors", len(vecs))
	}
	return vecs[0], nil
}

// lawyerMinSimilarity 读取站点设置；解析失败或越界时用默认值。
func lawyerMinSimilarity() float32 {
	raw := strings.TrimSpace(models.GetSiteSetting(lawyerCacheMinSimilarityKey, models.DefaultLawyerCacheMinSimilarity))
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < -1 || v > 1 {
		v, _ = strconv.ParseFloat(models.DefaultLawyerCacheMinSimilarity, 64)
	}
	return float32(v)
}

// searchLawyerCache 是 search_cache 的检索入口：有嵌入器时先向量检索，再用标签匹配补上还没有向量的条目；
// 嵌入失败或没有嵌入器时退回原有的标签子串匹配。
func searchLawyerCache(ctx context.Context, query string, topK int) []CacheMatch {
	emb, ok := lawyerEmbedderLoader()
	if !ok {
		return lawyerCache.Search(query, topK)
	}
	defer ensureLawyerCacheBackfill()

	qvec, err := emb.embedOne(ctx, query)
	if err != nil {
		alog.Warn("lawyer cache query embedding failed, falling back to tag match", "err", err)
		return lawyerCache.Search(query, topK)
	}
	matches, err := lawyerCache.SearchVector(emb.model, qvec, topK, lawyerMinSimilarity())
	if err != nil {
		alog.Warn("lawyer cache vector search failed, falling back to tag match", "err", err)
		return lawyerCache.Search(query, topK)
	}
	for _, m := range matches {
		alog.Debug("lawyer cache vector match", "query", query, "key", m.Key, "similarity", m.Similarity)
	}

	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		seen[m.Key] = true
	}
	for _, m := range lawyerCache.SearchTagsUnvectorized(emb.model, query, topK) {
		if len(matches) >= topK {
			break
		}
		if !seen[m.Key] {
			matches = append(matches, m)
		}
	}
	return matches
}

// saveLawyerCacheEntry 写入缓存条目；有嵌入器时同步算向量，失败则先存无向量条目，之后由补算填上。
func saveLawyerCacheEntry(ctx context.Context, key, ruling string) {
	emb, ok := lawyerEmbedderLoader()
	if !ok {
		lawyerCache.Set(key, ruling)
		return
	}
	vec, err := emb.embedOne(ctx, lawyerEmbedText(key, ruling))
	if err != nil {
		alog.Warn("lawyer cache entry embedding failed, saving without vector", "key", key, "err", err)
		lawyerCache.Set(key, ruling)
		return
	}
	lawyerCache.SetWithVector(key, ruling, vec, emb.model)
}

var (
	lawyerBackfillRunning atomic.Bool
	// lawyerBackfillDone 只给测试用：每轮补算结束时关闭并换新，测试据此等待结束。
	lawyerBackfillDone atomic.Pointer[chan struct{}]
)

// ensureLawyerCacheBackfill 在后台给缺少当前模型向量的条目补算，单飞：已有一轮在跑时直接返回。
// 某一批失败就记日志并结束本轮，下次触发再补。
func ensureLawyerCacheBackfill() {
	if !lawyerBackfillRunning.CompareAndSwap(false, true) {
		return
	}
	done := make(chan struct{})
	lawyerBackfillDone.Store(&done)
	go func() {
		defer close(done)
		defer lawyerBackfillRunning.Store(false)
		runLawyerBackfill()
	}()
}

func runLawyerBackfill() {
	emb, ok := lawyerEmbedderLoader()
	if !ok {
		return
	}
	missing := lawyerCache.MissingVectors(emb.model)
	if len(missing) == 0 {
		return
	}
	alog.Info("lawyer cache backfill started", "entries", len(missing), "model", emb.model)

	filled := 0
	for start := 0; start < len(missing); start += lawyerBackfillBatchSize {
		end := min(start+lawyerBackfillBatchSize, len(missing))
		batch := missing[start:end]
		texts := make([]string, len(batch))
		for i, e := range batch {
			texts[i] = lawyerEmbedText(e.Key, e.Value)
		}

		ctx, cancel := context.WithTimeout(context.Background(), lawyerBackfillBatchTimout)
		vecs, err := emb.embed(ctx, texts)
		cancel()
		if err != nil || len(vecs) != len(batch) {
			alog.Warn("lawyer cache backfill batch failed, will retry on next search", "filled", filled, "err", err)
			return
		}
		for i, e := range batch {
			if lawyerCache.AttachVector(e.Key, e.Value, vecs[i], emb.model) {
				filled++
			}
		}
	}
	alog.Info("lawyer cache backfill finished", "filled", filled, "model", emb.model)
}

// waitLawyerBackfill 等待当前补算结束，仅供测试使用。
func waitLawyerBackfill() {
	if p := lawyerBackfillDone.Load(); p != nil {
		<-*p
	}
}
