import type { CacheStats, LlmStatLine, LlmStats, TurnCacheStat } from '@/api/adminTypesCore'

// NOTE: 规则缓存 tab 的展示文本计算，口径与旧 admin.html 的 x-text 表达式逐项一致

export function pct1(rate: number): string {
  return (rate * 100).toFixed(1) + '%'
}

export function cacheTotal(s: CacheStats | null | undefined): number {
  return s ? s.full_hits + s.partial_hits + s.misses : 0
}

// NOTE: 命中率（0~1）；没有任何记录时为 null，界面据此隐藏命中率条
export function cacheHitRate(s: CacheStats | null | undefined): number | null {
  const total = cacheTotal(s)
  if (!s || total <= 0) return null
  return (s.full_hits + s.partial_hits) / total
}

export function hitRateText(s: CacheStats | null | undefined): string {
  const r = cacheHitRate(s)
  return r === null ? '-' : pct1(r)
}

export function hitRateWidth(s: CacheStats | null | undefined): number {
  const r = cacheHitRate(s)
  return r === null ? 0 : Math.min(100, r * 100)
}

export function capacityText(s: CacheStats | null | undefined): string {
  if (!s) return '-'
  return `${s.entries} 条 · ${(s.used_bytes / 1048576).toFixed(1)} MB / ${(s.max_bytes / 1073741824).toFixed(0)} GB`
}

// NOTE: 旧代码在 max_bytes 为 0 时得到 NaN 宽度；这里按 0 处理
export function capacityWidth(s: CacheStats | null | undefined): number {
  if (!s || !(s.max_bytes > 0)) return 0
  return Math.min(100, (s.used_bytes / s.max_bytes) * 100)
}

export function llmLineText(line: LlmStatLine): string {
  return (
    `${line.count} 次 · 均值 ${line.avg_ms.toFixed(0)}ms · 最大 ${line.max_ms}ms` +
    (line.err_count ? ` · 错误 ${line.err_count}` : '') +
    ` · 缓存命中 ${pct1(line.cache_hit_rate)}`
  )
}

export interface LlmOverallView {
  count: number
  avg: string
  max: string
  errors: number
  hitRate: string
}

export function llmOverallView(stats: LlmStats | null | undefined): LlmOverallView {
  const o = stats?.overall
  return {
    count: o?.count ?? 0,
    avg: (o?.avg_ms ?? 0).toFixed(0) + ' ms',
    max: (o?.max_ms ?? 0) + ' ms',
    errors: o?.err_count ?? 0,
    hitRate: pct1(o?.cache_hit_rate ?? 0),
  }
}

export function agentSummaryText(a: { turns: number; next_seq: number; estimated_runes: number }): string {
  return `${a.turns} 轮 · next_seq=${a.next_seq} · 约 ${a.estimated_runes} 字符`
}

export function turnStatLeft(s: TurnCacheStat): string {
  return `seq=${s.seq} round=${s.round}`
}

export function turnStatRight(s: TurnCacheStat): string {
  const hit = s.prompt_tokens > 0 ? ((s.cache_read_tokens / s.prompt_tokens) * 100).toFixed(1) : '0.0'
  return (
    `${s.calls}次 · prompt ${s.prompt_tokens} · 命中 ${hit}%` +
    (s.trimmed_turns ? ` · trim ${s.trimmed_turns}` : '') +
    (s.compacted_turns ? ` · 压缩 ${s.compacted_turns}` : '')
  )
}

// NOTE: 最近的轮次排在最前；不修改入参，recent_stats 可能是 null
export function recentStatsNewestFirst(stats: TurnCacheStat[] | null | undefined): TurnCacheStat[] {
  return (stats ?? []).slice().reverse()
}
