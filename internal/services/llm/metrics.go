// NOTE: 采集所有 LLM 调用（Chat/ChatStream/JsonChat/ChatWithTools/GenerateImage）的延迟，
// 按 角色(role) × 模型(model) × 方法(method) 聚合，内存累积并定期落库，供 admin 面板查看。
//
// 一条样本 = 一次 chat()/streamToResult() 调用的总耗时，含其内部对 5xx/网络错误的重试
// 与退避等待（重试对调用方是透明的，视为同一次往返的一部分）；外层 Chat/JsonChat/
// ChatWithTools 对空响应的重试会各自触发一次内部调用，因此产生多条独立样本；ChatStream
// 记录的是整个流式请求的总时长，不是首 token 延迟。这些语义会影响平均值的解读，
// 不代表"用户等待时间"。
package llm

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/llmcoc/server/internal/logging"
	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"
)

var log = logging.For("llm")

// truncateForLog 截断超长文本用于日志，避免 slog 的 TextHandler 把长文本转义成
// 单行巨长字符串刷屏（换行会被转成 \n 并整体加引号）。
func truncateForLog(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + fmt.Sprintf("...(truncated, total %d runes)", len(runes))
}

// statKey 是延迟统计的聚合维度。
type statKey struct {
	Role   string
	Model  string
	Method string
}

// statEntry 用 Count+SumMs 累积而非浮点滑动平均，避免精度随调用次数增长而漂移；
// 平均值在读取时用 SumMs/Count 现算。
type statEntry struct {
	Count    int64
	SumMs    int64
	ErrCount int64
	MaxMs    int64
}

func (e *statEntry) merge(o *statEntry) {
	e.Count += o.Count
	e.SumMs += o.SumMs
	e.ErrCount += o.ErrCount
	if o.MaxMs > e.MaxMs {
		e.MaxMs = o.MaxMs
	}
}

// maxStatKeys 是内存中允许存在的 (role,model,method) 组合数上限；超出后新 key 一律
// 折叠进 role="other"，防止 cacheKey 格式将来变化导致基数无限增长。
const maxStatKeys = 500

var (
	statsMu      sync.Mutex
	statsEntries = make(map[statKey]*statEntry)
)

// roleFromCacheKey 从 cacheKey 中提取调用方角色。cacheKey 格式约定为
// "<sessionID>:<role>[:后缀...]"（见 agent 包 agentHandle.cacheKey 及其各调用点），
// 第二段固定是角色，取不到时回落 "unknown"。GenerateImage 没有 cacheKey 参数，
// 调用方固定传 "painter"。
func roleFromCacheKey(key string) string {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) < 2 {
		return "unknown"
	}
	role := strings.TrimSpace(parts[1])
	if role == "" {
		return "unknown"
	}
	return role
}

// recordLatency 记录一次实际模型往返的延迟，err 非 nil 时计入错误计数但仍计入延迟样本。
func recordLatency(role, model, method string, elapsed time.Duration, err error) {
	ms := elapsed.Milliseconds()

	statsMu.Lock()
	defer statsMu.Unlock()

	key := statKey{Role: role, Model: model, Method: method}
	e, ok := statsEntries[key]
	if !ok && len(statsEntries) >= maxStatKeys {
		key = statKey{Role: "other", Model: model, Method: method}
		e, ok = statsEntries[key]
	}
	if !ok {
		e = &statEntry{}
		statsEntries[key] = e
	}
	e.Count++
	e.SumMs += ms
	if ms > e.MaxMs {
		e.MaxMs = ms
	}
	if err != nil {
		e.ErrCount++
	}
}

// StatLine 是单个聚合维度（整体/按角色/按模型）的延迟统计快照。
type StatLine struct {
	Key      string  `json:"key"`
	Count    int64   `json:"count"`
	AvgMs    float64 `json:"avg_ms"`
	MaxMs    int64   `json:"max_ms"`
	ErrCount int64   `json:"err_count"`
}

// StatsResult 是延迟统计的完整快照，供 admin API 直接序列化返回。
type StatsResult struct {
	Overall StatLine   `json:"overall"`
	ByRole  []StatLine `json:"by_role"`
	ByModel []StatLine `json:"by_model"`
}

func toLine(key string, e *statEntry) StatLine {
	avg := 0.0
	if e.Count > 0 {
		avg = float64(e.SumMs) / float64(e.Count)
	}
	return StatLine{Key: key, Count: e.Count, AvgMs: avg, MaxMs: e.MaxMs, ErrCount: e.ErrCount}
}

// Stats 返回当前延迟统计快照：整体聚合，以及按角色、按模型拆分的聚合（按调用次数降序）。
func Stats() StatsResult {
	statsMu.Lock()
	defer statsMu.Unlock()

	overall := &statEntry{}
	byRole := map[string]*statEntry{}
	byModel := map[string]*statEntry{}
	for k, e := range statsEntries {
		overall.merge(e)
		mergeGroup(byRole, k.Role, e)
		mergeGroup(byModel, k.Model, e)
	}

	result := StatsResult{
		Overall: toLine("overall", overall),
		ByRole:  toLines(byRole),
		ByModel: toLines(byModel),
	}
	return result
}

func mergeGroup(dst map[string]*statEntry, key string, e *statEntry) {
	agg, ok := dst[key]
	if !ok {
		agg = &statEntry{}
		dst[key] = agg
	}
	agg.merge(e)
}

func toLines(m map[string]*statEntry) []StatLine {
	lines := make([]StatLine, 0, len(m))
	for key, e := range m {
		lines = append(lines, toLine(key, e))
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].Count > lines[j].Count })
	return lines
}

// ResetStats 清空内存中的延迟统计,并删除已落库的历史数据。
func ResetStats() {
	statsMu.Lock()
	statsEntries = make(map[statKey]*statEntry)
	statsMu.Unlock()
	if err := models.DB.Where("1 = 1").Delete(&models.LLMLatencyStat{}).Error; err != nil {
		log.Error("reset stats: delete rows failed", "err", err)
	}
}

// LoadStats 把此前落库的延迟统计读回内存，应在数据库就绪后启动时调用一次。
func LoadStats() {
	var rows []models.LLMLatencyStat
	if err := models.DB.Find(&rows).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Error("load stats failed", "err", err)
		}
		return
	}
	statsMu.Lock()
	for _, r := range rows {
		statsEntries[statKey{Role: r.Role, Model: r.Model, Method: r.Method}] = &statEntry{
			Count: r.Count, SumMs: r.SumMs, ErrCount: r.ErrCount, MaxMs: r.MaxMs,
		}
	}
	statsMu.Unlock()
	log.Info("loaded llm latency stats", "rows", len(rows))
}

// StartStatsPersistence starts a background ticker that periodically writes the
// current in-memory latency statistics to the database. It returns a stop
// function that should be called on shutdown to flush the final snapshot.
func StartStatsPersistence(interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				persistStats()
			case <-done:
				ticker.Stop()
				persistStats()
				return
			}
		}
	}()
	return func() { close(done) }
}

// persistStats snapshots每个 (role,model,method) 组合的当前计数，按唯一键 upsert
// 进 LLMLatencyStat 表。
func persistStats() {
	statsMu.Lock()
	snapshot := make(map[statKey]statEntry, len(statsEntries))
	for k, e := range statsEntries {
		snapshot[k] = *e
	}
	statsMu.Unlock()

	for k, e := range snapshot {
		err := models.DB.Where(models.LLMLatencyStat{Role: k.Role, Model: k.Model, Method: k.Method}).
			Assign(models.LLMLatencyStat{
				Count: e.Count, SumMs: e.SumMs, ErrCount: e.ErrCount, MaxMs: e.MaxMs, SavedAt: time.Now(),
			}).
			FirstOrCreate(&models.LLMLatencyStat{}).Error
		if err != nil {
			log.Error("persist stats error", "role", k.Role, "model", k.Model, "method", k.Method, "err", err)
		}
	}
}
