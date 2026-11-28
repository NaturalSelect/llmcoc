// NOTE: 分类器记录（System One 的输入分类与 NSFW 路由）的纯逻辑：入参规整、按 id 合并、筛选排序、中文标签与格式化。
// 不依赖 DOM、Vue 和网络；roomState 用它归约 classifier_log 事件，分类器面板用它展示。记录里的文本来自玩家和 KP，一律当不可信文本，这里不做任何 HTML 处理
import type { ClassifierKind, ClassifierLog } from '@/api/classifierTypes'
import { fmtDuration } from '@/utils/format'

// NOTE: 房间里最多保留最近 500 条（按 id 取最大的 500 条），和历史接口的上限一致
export const CLASSIFIER_LOG_LIMIT = 500

const KINDS: readonly string[] = ['input_guard', 'nsfw_route']

export type ClassifierFilter = 'all' | ClassifierKind

export const CLASSIFIER_FILTERS: ReadonlyArray<{ key: ClassifierFilter; label: string }> = [
  { key: 'all', label: '全部' },
  { key: 'input_guard', label: '输入分类' },
  { key: 'nsfw_route', label: 'NSFW 路由' },
]

const KIND_LABELS: Record<string, string> = { input_guard: '输入分类', nsfw_route: 'NSFW 路由' }

// NOTE: 选项名是分类器自己定义的键，未知的原样显示，后端加了新选项也不会空白
const OPTION_LABELS: Record<string, string> = {
  in_character: '角色内行动',
  table_talk: '桌边闲聊',
  authority_claim: '自称权威',
  spoiler_request: '索要谜底',
  prompt_injection: '提示词注入',
  sexual_explicit: '露骨性内容',
  sexual_suggestive: '性暗示',
  non_sexual: '无性内容',
}

const DECISION_LABELS: Record<string, string> = {
  blocked: '已拒收',
  passed: '放行',
  nsfw: '涉及 NSFW',
  clean: '不涉及',
  failed: '失败',
}

const ROLE_LABELS: Record<string, string> = { director: 'Director', director_nsfw: 'NSFW Director' }

// NOTE: 取值与 StampBadge 的 tone 一致，面板直接传给徽标
export type ClassifierTone = 'danger' | 'seal' | 'warning' | 'moss' | 'faint'

export interface ProbabilityRow {
  name: string
  label: string
  value: number
  // NOTE: 横条宽度，0 到 100
  percent: number
  // NOTE: 例如 55.0%
  text: string
  // NOTE: 概率最高的一项
  top: boolean
}

export interface RequestEntry {
  key: string
  value: string
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

function num(v: unknown): number {
  const n = typeof v === 'number' ? v : Number(v)
  return Number.isFinite(n) ? n : 0
}

// NOTE: request 的值契约上都是字符串；防御性地把别的类型转成文本，null / undefined 跳过
function textOf(v: unknown): string | null {
  if (v == null) return null
  if (typeof v === 'string') return v
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

// NOTE: 来自 WebSocket 和历史接口的原始 JSON 都经过这里。id 或 kind 不合法的丢弃（返回 null），其余字段缺省时补默认值，不抛错
export function normalizeClassifierLog(raw: unknown): ClassifierLog | null {
  if (!isRecord(raw)) return null
  const id = Number(raw.id)
  if (!Number.isSafeInteger(id) || id <= 0) return null
  if (typeof raw.kind !== 'string' || !KINDS.includes(raw.kind)) return null

  const probabilities: Record<string, number> = {}
  if (isRecord(raw.probabilities)) {
    for (const [name, v] of Object.entries(raw.probabilities)) {
      const n = typeof v === 'number' ? v : Number.NaN
      if (Number.isFinite(n)) probabilities[name] = n
    }
  }
  const request: Record<string, string> = {}
  if (isRecord(raw.request)) {
    for (const [key, v] of Object.entries(raw.request)) {
      const t = textOf(v)
      if (t !== null) request[key] = t
    }
  }

  return {
    id,
    kind: raw.kind as ClassifierKind,
    created_at: str(raw.created_at),
    user_id: num(raw.user_id),
    subject: str(raw.subject),
    floor: num(raw.floor),
    decision: str(raw.decision),
    status: raw.status === 'error' ? 'error' : 'ok',
    error: str(raw.error),
    choice: str(raw.choice),
    score: num(raw.score),
    threshold: num(raw.threshold),
    probabilities,
    request,
    role: str(raw.role),
    elapsed_ms: num(raw.elapsed_ms),
    request_id: str(raw.request_id),
  }
}

// NOTE: 历史接口的 logs 在没有记录时可能是 null
export function normalizeClassifierLogs(raw: unknown): ClassifierLog[] {
  const out: ClassifierLog[] = []
  if (!Array.isArray(raw)) return out
  for (const item of raw) {
    const log = normalizeClassifierLog(item)
    if (log) out.push(log)
  }
  return out
}

// NOTE: 原地并入 list（Vue 的 reactive 数组必须原地改才会触发渲染，所以不返回新数组）：按 id 去重（已有的不覆盖），保持 id 升序，
// 超过 limit 条时丢掉 id 最小的。历史接口和实时事件会重叠，也可能乱序到达，所以这几种情况都要成立
export function mergeClassifierLogs(list: ClassifierLog[], incoming: readonly ClassifierLog[], limit = CLASSIFIER_LOG_LIMIT): void {
  let seen: Set<number> | null = null
  for (const log of incoming) {
    const last = list.length > 0 ? list[list.length - 1].id : 0
    if (log.id > last) {
      list.push(log)
      seen?.add(log.id)
      continue
    }
    // NOTE: 慢路径：id 不比末尾新，可能是重复也可能是乱序的旧记录
    if (seen === null) seen = new Set(list.map((l) => l.id))
    if (seen.has(log.id)) continue
    let i = list.length
    while (i > 0 && list[i - 1].id > log.id) i--
    list.splice(i, 0, log)
    seen.add(log.id)
  }
  if (list.length > limit) list.splice(0, list.length - limit)
}

// NOTE: 列表展示：按 kind 筛选，新的在上
export function visibleLogs(logs: readonly ClassifierLog[], filter: string): ClassifierLog[] {
  const list = filter === 'all' ? logs.slice() : logs.filter((l) => l.kind === filter)
  return list.sort((a, b) => b.id - a.id)
}

export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? (kind || '未知')
}

export function optionLabel(name: string): string {
  return OPTION_LABELS[name] ?? name
}

// NOTE: status=error 的 passed（输入分类出错时的放行）要让人一眼看出不是真的判了放行，所以补一个「（出错）」
export function decisionLabel(log: Pick<ClassifierLog, 'decision' | 'status'>): string {
  const base = DECISION_LABELS[log.decision] ?? (log.decision || '未知')
  return log.status === 'error' && log.decision !== 'failed' ? `${base}（出错）` : base
}

// NOTE: blocked 和 nsfw 醒目；出错和 failed 警示；passed 和 clean 平和
export function decisionTone(log: Pick<ClassifierLog, 'decision' | 'status'>): ClassifierTone {
  if (log.status === 'error') return 'warning'
  switch (log.decision) {
    case 'blocked':
      return 'danger'
    case 'nsfw':
      return 'seal'
    case 'failed':
      return 'warning'
    case 'passed':
    case 'clean':
      return 'moss'
    default:
      return 'faint'
  }
}

// NOTE: score >= threshold 为命中；出错时没有有效分数，不算命中
export function isHit(log: Pick<ClassifierLog, 'status' | 'score' | 'threshold'>): boolean {
  return log.status !== 'error' && log.score >= log.threshold
}

export function verdictText(log: Pick<ClassifierLog, 'status' | 'score' | 'threshold'>): string {
  if (log.status === 'error') return '没有得到有效分数'
  return isHit(log) ? '命中（分数 ≥ 阈值）' : '未命中（分数 < 阈值）'
}

// NOTE: 两位小数，非有限数显示破折号
export function fmt2(n: number): string {
  return Number.isFinite(n) ? n.toFixed(2) : '—'
}

// NOTE: 一行里的「分数 / 阈值」，保留两位小数；出错时分数是占位的 0，不显示
export function fmtScore(log: Pick<ClassifierLog, 'status' | 'score' | 'threshold'>): string {
  if (log.status === 'error') return '—'
  return `${fmt2(log.score)} / ${fmt2(log.threshold)}`
}

export function fmtPercent(v: number): string {
  return Number.isFinite(v) ? `${(v * 100).toFixed(1)}%` : '—'
}

// NOTE: 横条宽度：概率换成 0 到 100 的百分数，保留一位小数
export function barPercent(v: number): number {
  if (!Number.isFinite(v)) return 0
  return Math.max(0, Math.min(100, Math.round(v * 1000) / 10))
}

// NOTE: 降序排列；同分按名字排，保证顺序稳定。最高项以后端给的 choice 为准（它在概率表里才算），否则取排第一的
export function probabilityRows(probabilities: Record<string, number> | null | undefined, choice = ''): ProbabilityRow[] {
  const entries = Object.entries(probabilities ?? {}).filter(([, v]) => Number.isFinite(v))
  entries.sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0))
  const topName = entries.some(([name]) => name === choice) ? choice : entries[0]?.[0]
  return entries.map(([name, value]) => ({
    name,
    label: optionLabel(name),
    value,
    percent: barPercent(value),
    text: fmtPercent(value),
    top: name === topName,
  }))
}

export function requestEntries(request: Record<string, string> | null | undefined): RequestEntry[] {
  return Object.entries(request ?? {}).map(([key, value]) => ({ key, value }))
}

// NOTE: 只有 nsfw_route 才有「本周期执行的角色」；没有角色时返回空串，面板据此不显示这一行
export function executionNote(log: Pick<ClassifierLog, 'kind' | 'role'>): string {
  if (log.kind !== 'nsfw_route' || !log.role) return ''
  return `本周期执行：${ROLE_LABELS[log.role] ?? log.role}`
}

function parseDate(iso: string | null | undefined): Date | null {
  if (!iso) return null
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? null : d
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

// NOTE: 本地时间 HH:MM:SS
export function fmtClock(iso: string | null | undefined): string {
  const d = parseDate(iso)
  return d ? `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}` : ''
}

// NOTE: 本地时间 YYYY-MM-DD HH:MM:SS
export function fmtDateTime(iso: string | null | undefined): string {
  const d = parseDate(iso)
  return d ? `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${fmtClock(iso)}` : ''
}

// NOTE: fmtDuration 把 0 当成没有值返回空串；这里 0ms 是有意义的数字
export function fmtElapsed(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms) || ms < 0) return '—'
  return ms === 0 ? '0ms' : fmtDuration(Math.round(ms))
}
