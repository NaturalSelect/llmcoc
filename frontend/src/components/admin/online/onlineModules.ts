import type {
  BrowseItem,
  CnmodsItem,
  OnlineModuleDocument,
  OnlineModuleMeta,
  OnlineStatus,
} from '@/api/adminTypesOnline'

// NOTE: 线上模组页面的纯逻辑：文案、默认选中、编译禁用原因、命中片段切分。不碰 DOM 和网络，方便单测

// 与后端 onlineCompileMaxBytes 一致（界面上写作 500KB）
export const MAX_COMPILE_BYTES = 512 * 1024
export const MAX_ARCHIVE_BYTES = 50 * 1024 * 1024
// 详情接口里每篇文档只带前 300 字预览（与后端一致）；字数不超过它时预览就是全文
export const PREVIEW_RUNES = 300

const NAMED_ENTITIES: Record<string, string> = {
  nbsp: ' ',
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  apos: "'",
  ldquo: '\u201c',
  rdquo: '\u201d',
  lsquo: '\u2018',
  rsquo: '\u2019',
  hellip: '\u2026',
  mdash: '\u2014',
  ndash: '\u2013',
}

function decodeEntity(whole: string, body: string): string {
  if (body[0] === '#') {
    const code = body[1] === 'x' || body[1] === 'X' ? parseInt(body.slice(2), 16) : parseInt(body.slice(1), 10)
    if (!Number.isFinite(code) || code <= 0 || code > 0x10ffff) return whole
    return String.fromCodePoint(code)
  }
  return NAMED_ENTITIES[body.toLowerCase()] ?? whole
}

// 来源站的 opinion 是 HTML，这里只取纯文本。先去标签再解码实体，所以 &lt;b&gt; 这类转义内容会保留为文字，不会被当成标签
export function opinionText(html: string | null | undefined): string {
  if (!html) return ''
  return html
    .replace(/<(script|style)\b[\s\S]*?<\/\1\s*>/gi, '')
    .replace(/<(?:br|\/p|\/div|\/li|\/h[1-6])\b[^>]*>/gi, ' ')
    .replace(/<\/?[a-zA-Z!][^>]*>/g, '')
    .replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, decodeEntity)
    .replace(/\s+/g, ' ')
    .trim()
}

// 没有本地记录或仍是 pending 都算「未下载」；已有 scenario_id 说明已编译入库，优先显示
export function statusLabel(status: OnlineStatus | null | undefined, scenarioId?: number | null): string {
  if (scenarioId != null) return '已编译'
  switch (status) {
    case 'downloading':
      return '下载中'
    case 'extracted':
      return '已提取'
    case 'failed':
      return '失败'
    default:
      return '未下载'
  }
}

export type StatusTone = 'seal' | 'ledger' | 'moss' | 'faint' | 'danger'

export function statusTone(status: OnlineStatus | null | undefined, scenarioId?: number | null): StatusTone {
  if (scenarioId != null) return 'seal'
  switch (status) {
    case 'downloading':
      return 'ledger'
    case 'extracted':
      return 'moss'
    case 'failed':
      return 'danger'
    default:
      return 'faint'
  }
}

// 取字数最多的候选文档；没有候选就取字数最多的文档；字数相同取排在前面的
export function defaultDocument<T extends Pick<OnlineModuleDocument, 'runes' | 'candidate'>>(docs: readonly T[]): T | null {
  const longest = (list: readonly T[]) => list.reduce<T | null>((best, d) => (best === null || d.runes > best.runes ? d : best), null)
  return longest(docs.filter((d) => d.candidate)) ?? longest(docs)
}

export function utf8Bytes(text: string): number {
  return new TextEncoder().encode(text).length
}

// NOTE: 字节数口径选「加载全文后用 TextEncoder 的实际字节数」，不用 runes * 3 估算：估算会把英文/混排的
// 17.4 万字以上正文误禁用，而后端按实际字节数（len(text) > 512KB）才返回 413。
// 详情接口只有预览，所以字节数 >= 字数这条下界可以先挡掉明显超限的；只有 runes * 3 > 上限 且 runes <= 上限
// 的区间无法确定，此时界面自动加载全文，算出 textBytes 再传给 compileBlockReason，加载完成前禁用编译
export function bytesAmbiguous(doc: Pick<OnlineModuleDocument, 'runes'>): boolean {
  return doc.runes * 3 > MAX_COMPILE_BYTES && doc.runes <= MAX_COMPILE_BYTES
}

export interface CompileBlockModule {
  status: OnlineStatus
  license_override_at: string | null
}

// 返回编译按钮禁用的原因；null 表示可以编译。textBytes 是已加载全文的 UTF-8 字节数；
// 未加载（undefined）时只用「字节数 >= 字数」的下界判断，不做 runes * 3 估算
export function compileBlockReason(
  module: CompileBlockModule | null | undefined,
  doc: Pick<OnlineModuleDocument, 'runes' | 'license_hits' | 'garbled'> | null | undefined,
  textBytes?: number | null,
): string | null {
  if (!module || module.status !== 'extracted' || !doc) return '未提取'
  if (doc.garbled) return '所选文档的文字提取结果是乱码'
  if ((doc.license_hits?.length ?? 0) > 0 && module.license_override_at == null) return '所选文档命中版权筛查，需先放行'
  const tooLarge = textBytes != null ? textBytes > MAX_COMPILE_BYTES : doc.runes > MAX_COMPILE_BYTES
  if (tooLarge) return '正文超过 500KB'
  return null
}

export interface TextSegment {
  text: string
  hit: boolean
}

// 把命中片段在正文里的出现位置切成高亮段。命中片段不一定逐字出现在正文里（提取时可能规整过空白），找不到就不高亮；
// maxMatches 防止全文很长、命中片段很短时生成海量节点
export function splitByHits(text: string, hits: readonly string[] | null | undefined, maxMatches = 200): TextSegment[] {
  if (!text) return []
  const needles = [...new Set((hits ?? []).map((h) => h.trim()).filter(Boolean))]
  const ranges: Array<[number, number]> = []
  for (const needle of needles) {
    let from = 0
    while (ranges.length < maxMatches) {
      const at = text.indexOf(needle, from)
      if (at < 0) break
      ranges.push([at, at + needle.length])
      from = at + needle.length
    }
  }
  if (ranges.length === 0) return [{ text, hit: false }]

  ranges.sort((a, b) => a[0] - b[0] || b[1] - a[1])
  const merged: Array<[number, number]> = []
  for (const r of ranges) {
    const last = merged[merged.length - 1]
    if (last && r[0] <= last[1]) last[1] = Math.max(last[1], r[1])
    else merged.push([r[0], r[1]])
  }

  const out: TextSegment[] = []
  let cursor = 0
  for (const [start, end] of merged) {
    if (start > cursor) out.push({ text: text.slice(cursor, start), hit: false })
    out.push({ text: text.slice(start, end), hit: true })
    cursor = end
  }
  if (cursor < text.length) out.push({ text: text.slice(cursor), hit: false })
  return out
}

// 全文可能有数 MB，整段塞进页面会卡；只截断显示，不影响编译用的正文
export function clipForDisplay(text: string, max = 200_000): { text: string; clipped: boolean } {
  return text.length > max ? { text: text.slice(0, max), clipped: true } : { text, clipped: false }
}

export function formatBytes(n: number | null | undefined): string {
  const v = Number(n || 0)
  if (v < 1024) return v + ' B'
  if (v < 1024 * 1024) return (v / 1024).toFixed(1) + ' KB'
  return (v / 1024 / 1024).toFixed(1) + ' MB'
}

function rangeText(min?: number, max?: number, suffix = ''): string {
  const lo = Number(min || 0)
  const hi = Number(max || 0)
  if (!lo && !hi) return ''
  if (!hi || lo === hi) return (lo || hi) + suffix
  if (!lo) return hi + suffix
  return lo + '-' + hi + suffix
}

interface MetaParts {
  age?: string
  structure?: string
  minAmount?: number
  maxAmount?: number
  minDuration?: number
  maxDuration?: number
  version?: string
  place?: string
}

function joinMeta(m: MetaParts): string {
  const people = rangeText(m.minAmount, m.maxAmount, '人')
  // 来源站没有给出时长单位，这里只显示数值，不猜单位
  const duration = rangeText(m.minDuration, m.maxDuration)
  return [m.age, m.structure, people, duration ? '时长 ' + duration : '', m.place, m.version ? 'v' + m.version.replace(/^v/i, '') : '']
    .filter((s): s is string => !!s)
    .join(' · ')
}

export function cnmodsMetaLine(item: CnmodsItem): string {
  return joinMeta({
    age: item.moduleAge,
    structure: item.structure,
    minAmount: item.minAmount,
    maxAmount: item.maxAmount,
    minDuration: item.minDuration,
    maxDuration: item.maxDuration,
    version: item.moduleVersion,
    place: item.occurrencePlace,
  })
}

export function moduleMetaLine(meta: OnlineModuleMeta | null | undefined): string {
  if (!meta) return ''
  return joinMeta({
    age: meta.module_age,
    structure: meta.structure,
    minAmount: meta.min_amount,
    maxAmount: meta.max_amount,
    minDuration: meta.min_duration,
    maxDuration: meta.max_duration,
    version: meta.version,
    place: meta.place,
  })
}

// 作者与发布日期；来源站没有给出的字段直接省略
export function cnmodsByline(item: Pick<CnmodsItem, 'article' | 'releaseDate'>): string {
  const date = item.releaseDate ? String(item.releaseDate).slice(0, 10) : ''
  return [item.article ? '作者 ' + item.article : '', date].filter(Boolean).join(' · ')
}

// 下载接口的请求体是来源站条目原样，不能带上浏览接口附加的 local
export function toCnmodsItem(item: BrowseItem): CnmodsItem {
  const copy: Partial<BrowseItem> = { ...item }
  delete copy.local
  return copy as CnmodsItem
}

export function validateArchiveFile(file: { name: string; size: number }): string | null {
  if (!/\.(zip|docx|pdf)$/i.test(file.name)) return '仅支持 .zip / .docx / .pdf 格式'
  if (file.size <= 0) return '文件内容为空'
  if (file.size > MAX_ARCHIVE_BYTES) return '文件过大（最大 50MB）'
  return null
}

// 分享页地址来自来源站，只放行 http(s)，避免 javascript: 之类的地址被渲染成链接
export function safeUrl(url: string | null | undefined): string {
  const u = (url ?? '').trim()
  return /^https?:\/\//i.test(u) ? u : ''
}

export function formatTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('zh-CN', { hour12: false })
}

export const DOWNLOAD_FAILED_HINT = '自动下载失败，请打开分享页手动下载后上传'

export const RIGHTS_NOTICE = 'cnmods 模组作者免费发布，不可商用，多数禁止二改二传，仅限本站跑团使用，不要对外分发。'
