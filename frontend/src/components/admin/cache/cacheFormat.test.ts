import { describe, expect, test } from 'vitest'
import type { CacheStats, LlmStatLine } from '@/api/adminTypesCore'
import {
  agentSummaryText,
  cacheHitRate,
  capacityText,
  capacityWidth,
  hitRateText,
  hitRateWidth,
  llmLineText,
  llmOverallView,
  recentStatsNewestFirst,
  turnStatLeft,
  turnStatRight,
} from './cacheFormat'

const GB = 1073741824

function stats(over: Partial<CacheStats> = {}): CacheStats {
  return { full_hits: 6, partial_hits: 2, misses: 2, entries: 12, used_bytes: 5 * 1048576, max_bytes: 4 * GB, ...over }
}

describe('规则缓存统计', () => {
  test('命中率 = (完全+部分) / 总数，保留 1 位小数', () => {
    expect(cacheHitRate(stats())).toBe(0.8)
    expect(hitRateText(stats())).toBe('80.0%')
    expect(hitRateText(stats({ full_hits: 1, partial_hits: 0, misses: 2 }))).toBe('33.3%')
    expect(hitRateWidth(stats())).toBeCloseTo(80)
  })

  test('没有任何计数或没有数据时没有命中率', () => {
    expect(cacheHitRate(stats({ full_hits: 0, partial_hits: 0, misses: 0 }))).toBeNull()
    expect(hitRateText(null)).toBe('-')
    expect(hitRateWidth(null)).toBe(0)
  })

  test('容量文案：条数 · 已用 MB / 上限 GB', () => {
    expect(capacityText(stats())).toBe('12 条 · 5.0 MB / 4 GB')
    expect(capacityText(null)).toBe('-')
  })

  test('容量条宽度夹在 0~100，上限为 0 时不产生 NaN', () => {
    expect(capacityWidth(stats({ used_bytes: GB, max_bytes: 4 * GB }))).toBe(25)
    expect(capacityWidth(stats({ used_bytes: 9 * GB }))).toBe(100)
    expect(capacityWidth(stats({ max_bytes: 0 }))).toBe(0)
    expect(capacityWidth(null)).toBe(0)
  })
})

describe('LLM 调用统计', () => {
  const line: LlmStatLine = {
    key: 'director',
    count: 10,
    avg_ms: 1234.56,
    max_ms: 5000,
    err_count: 0,
    prompt_tokens: 100,
    output_tokens: 10,
    cache_read_tokens: 50,
    cache_creation_tokens: 0,
    cache_hit_rate: 0.5,
  }

  test('分行文案：无错误时省略错误段，有错误时带上', () => {
    expect(llmLineText(line)).toBe('10 次 · 均值 1235ms · 最大 5000ms · 缓存命中 50.0%')
    expect(llmLineText({ ...line, err_count: 3, cache_hit_rate: 0.123 })).toBe('10 次 · 均值 1235ms · 最大 5000ms · 错误 3 · 缓存命中 12.3%')
  })

  test('整体概览：字段缺失时全部回落为 0', () => {
    expect(llmOverallView(null)).toStrictEqual({ count: 0, avg: '0 ms', max: '0 ms', errors: 0, hitRate: '0.0%' })
    expect(llmOverallView({ overall: line })).toStrictEqual({ count: 10, avg: '1235 ms', max: '5000 ms', errors: 0, hitRate: '50.0%' })
  })
})

describe('会话上下文', () => {
  test('智能体摘要与逐轮统计文案', () => {
    expect(agentSummaryText({ turns: 4, next_seq: 9, estimated_runes: 12345 })).toBe('4 轮 · next_seq=9 · 约 12345 字符')
    const s = { seq: 3, round: 2, calls: 5, prompt_tokens: 2000, cache_read_tokens: 1500 }
    expect(turnStatLeft(s)).toBe('seq=3 round=2')
    expect(turnStatRight(s)).toBe('5次 · prompt 2000 · 命中 75.0%')
    expect(turnStatRight({ ...s, trimmed_turns: 2, compacted_turns: 1 })).toBe('5次 · prompt 2000 · 命中 75.0% · trim 2 · 压缩 1')
  })

  test('prompt 为 0 时命中率显示 0.0%，不出现 NaN', () => {
    expect(turnStatRight({ seq: 1, round: 1, calls: 1, prompt_tokens: 0, cache_read_tokens: 0 })).toBe('1次 · prompt 0 · 命中 0.0%')
  })

  test('最近轮次倒序且不修改原数组，null 视为空', () => {
    const src = [
      { seq: 1, round: 1, calls: 1, prompt_tokens: 1, cache_read_tokens: 0 },
      { seq: 2, round: 1, calls: 1, prompt_tokens: 1, cache_read_tokens: 0 },
    ]
    expect(recentStatsNewestFirst(src).map((s) => s.seq)).toStrictEqual([2, 1])
    expect(src.map((s) => s.seq)).toStrictEqual([1, 2])
    expect(recentStatsNewestFirst(null)).toStrictEqual([])
  })
})
