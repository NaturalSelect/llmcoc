import { describe, expect, test } from 'vitest'
import type { BrowseItem, OnlineModuleDocument, OnlineStatus } from '@/api/adminTypesOnline'
import {
  MAX_COMPILE_BYTES,
  bytesAmbiguous,
  clipForDisplay,
  cnmodsByline,
  cnmodsMetaLine,
  compileBlockReason,
  defaultDocument,
  formatBytes,
  formatElapsed,
  moduleMetaLine,
  opinionText,
  safeUrl,
  sortTaskModules,
  splitByHits,
  stageLabel,
  statusLabel,
  statusTone,
  taskElapsedMs,
  toCnmodsItem,
  utf8Bytes,
  validateArchiveFile,
} from './onlineModules'

const doc = (over: Partial<OnlineModuleDocument> = {}): OnlineModuleDocument => ({
  id: 1,
  online_module_id: 1,
  path: 'a.docx',
  kind: 'docx',
  runes: 1000,
  candidate: true,
  ocr: false,
  garbled: false,
  license_hits: [],
  ...over,
})

describe('opinionText', () => {
  test('去标签、解码常见实体、压缩空白', () => {
    expect(opinionText('<p>第一行&nbsp;&amp;&nbsp;第二行</p>\n\n<p>  &ldquo;引号&rdquo;  </p>')).toBe('第一行 & 第二行 “引号”')
    expect(opinionText('a<br/>b<br>c')).toBe('a b c')
    expect(opinionText('&#20320;&#x597d;&lt;3')).toBe('你好<3')
  })

  test('转义后的尖括号保留为文字；script / style 整段丢弃；未知实体原样保留', () => {
    expect(opinionText('&lt;b&gt;加粗&lt;/b&gt;')).toBe('<b>加粗</b>')
    expect(opinionText('x<script>alert(1)</script>y<style>p{}</style>z')).toBe('xyz')
    expect(opinionText('a &unknown; b')).toBe('a &unknown; b')
    expect(opinionText('&#99999999;')).toBe('&#99999999;')
  })

  test('空值返回空串', () => {
    expect(opinionText('')).toBe('')
    expect(opinionText(null)).toBe('')
    expect(opinionText(undefined)).toBe('')
    expect(opinionText('<p> </p>')).toBe('')
  })
})

describe('statusLabel / statusTone', () => {
  test('四种状态加已编译', () => {
    expect(statusLabel('pending')).toBe('未下载')
    expect(statusLabel(undefined)).toBe('未下载')
    expect(statusLabel(null)).toBe('未下载')
    expect(statusLabel('downloading')).toBe('下载中')
    expect(statusLabel('extracted')).toBe('已提取')
    expect(statusLabel('failed')).toBe('失败')
    expect(statusLabel('extracted', 7)).toBe('已编译')
    expect(statusLabel('failed', 0)).toBe('已编译')
    expect(statusLabel('extracted', null)).toBe('已提取')
  })

  test('色调与状态对应', () => {
    expect(statusTone('failed')).toBe('danger')
    expect(statusTone('extracted')).toBe('moss')
    expect(statusTone('downloading')).toBe('ledger')
    expect(statusTone(undefined)).toBe('faint')
    expect(statusTone('extracted', 3)).toBe('seal')
  })
})

describe('defaultDocument', () => {
  test('优先取字数最多的候选文档，即使非候选更长', () => {
    const docs = [doc({ id: 1, runes: 9000, candidate: false }), doc({ id: 2, runes: 500 }), doc({ id: 3, runes: 800 })]
    expect(defaultDocument(docs)?.id).toBe(3)
  })

  test('没有候选就取字数最多的', () => {
    const docs = [doc({ id: 1, runes: 100, candidate: false }), doc({ id: 2, runes: 300, candidate: false })]
    expect(defaultDocument(docs)?.id).toBe(2)
  })

  test('字数相同取靠前的；空数组返回 null', () => {
    expect(defaultDocument([doc({ id: 1, runes: 5 }), doc({ id: 2, runes: 5 })])?.id).toBe(1)
    expect(defaultDocument([])).toBeNull()
  })
})

describe('compileBlockReason', () => {
  const ok = { status: 'extracted' as const, license_override_at: null }

  test('未提取：模组不是 extracted 或没有文档', () => {
    expect(compileBlockReason({ status: 'failed', license_override_at: null }, doc())).toBe('未提取')
    expect(compileBlockReason({ status: 'downloading', license_override_at: null }, doc())).toBe('未提取')
    expect(compileBlockReason(ok, null)).toBe('未提取')
    expect(compileBlockReason(null, doc())).toBe('未提取')
  })

  test('命中版权且未放行时禁用，放行后可编译', () => {
    const hit = doc({ license_hits: ['禁止投喂AI'] })
    expect(compileBlockReason(ok, hit)).toBe('所选文档命中版权筛查，需先放行')
    expect(compileBlockReason({ status: 'extracted', license_override_at: '2026-11-14T08:00:00Z' }, hit)).toBeNull()
    // 放行判定只看 license_override_at 是否为空
    expect(compileBlockReason({ status: 'extracted', license_override_at: '' }, hit)).toBeNull()
  })

  test('没有命中的文档不需要放行', () => {
    expect(compileBlockReason(ok, doc({ license_hits: [] }))).toBeNull()
  })

  test('疑似乱码的文档一律禁用，放行版权也不行', () => {
    const garbled = doc({ garbled: true })
    expect(compileBlockReason(ok, garbled)).toBe('所选文档的文字提取结果是乱码')
    const hitAndGarbled = doc({ garbled: true, license_hits: ['禁止投喂AI'] })
    expect(compileBlockReason({ status: 'extracted', license_override_at: '2026-11-14T08:00:00Z' }, hitAndGarbled)).toBe(
      '所选文档的文字提取结果是乱码',
    )
  })

  test('OCR 识别出的文档不受影响', () => {
    expect(compileBlockReason(ok, doc({ ocr: true }))).toBeNull()
  })

  test('未加载全文：字数已超 500KB 必然超限，其余不拦', () => {
    expect(compileBlockReason(ok, doc({ runes: MAX_COMPILE_BYTES + 1 }))).toBe('正文超过 500KB')
    expect(compileBlockReason(ok, doc({ runes: MAX_COMPILE_BYTES }))).toBeNull()
    expect(compileBlockReason(ok, doc({ runes: 300_000 }))).toBeNull()
  })

  test('已加载全文：按实际字节数判断，与字数无关', () => {
    expect(compileBlockReason(ok, doc({ runes: 300_000 }), MAX_COMPILE_BYTES)).toBeNull()
    expect(compileBlockReason(ok, doc({ runes: 300_000 }), MAX_COMPILE_BYTES + 1)).toBe('正文超过 500KB')
    expect(compileBlockReason(ok, doc({ runes: 10 }), 0)).toBeNull()
  })

  test('原因按优先级：未提取 > 版权 > 体积', () => {
    const big = doc({ runes: MAX_COMPILE_BYTES + 1, license_hits: ['x'] })
    expect(compileBlockReason(ok, big)).toBe('所选文档命中版权筛查，需先放行')
    expect(compileBlockReason({ status: 'pending', license_override_at: null }, big)).toBe('未提取')
  })
})

describe('bytesAmbiguous', () => {
  test('只有 runes * 3 超限且 runes 本身不超限时需要看真实字节数', () => {
    expect(bytesAmbiguous({ runes: 1000 })).toBe(false)
    expect(bytesAmbiguous({ runes: Math.floor(MAX_COMPILE_BYTES / 3) })).toBe(false)
    expect(bytesAmbiguous({ runes: Math.floor(MAX_COMPILE_BYTES / 3) + 1 })).toBe(true)
    expect(bytesAmbiguous({ runes: MAX_COMPILE_BYTES })).toBe(true)
    expect(bytesAmbiguous({ runes: MAX_COMPILE_BYTES + 1 })).toBe(false)
  })
})

describe('utf8Bytes', () => {
  test('按 UTF-8 字节计数', () => {
    expect(utf8Bytes('abc')).toBe(3)
    expect(utf8Bytes('雨夜')).toBe(6)
    expect(utf8Bytes('')).toBe(0)
  })
})

describe('splitByHits', () => {
  test('命中片段切成高亮段，其余保持原文', () => {
    expect(splitByHits('前文禁止投喂AI后文', ['禁止投喂AI'])).toStrictEqual([
      { text: '前文', hit: false },
      { text: '禁止投喂AI', hit: true },
      { text: '后文', hit: false },
    ])
  })

  test('多处出现与相互重叠的片段合并', () => {
    expect(splitByHits('abcXabc', ['abc']).filter((s) => s.hit)).toHaveLength(2)
    // 紧挨着的两处命中合成一段高亮
    expect(splitByHits('abcabc', ['abc'])).toStrictEqual([{ text: 'abcabc', hit: true }])
    expect(splitByHits('0123456789', ['2345', '4567'])).toStrictEqual([
      { text: '01', hit: false },
      { text: '234567', hit: true },
      { text: '89', hit: false },
    ])
  })

  test('找不到、空片段、空正文', () => {
    expect(splitByHits('正文', ['不存在'])).toStrictEqual([{ text: '正文', hit: false }])
    expect(splitByHits('正文', ['', '  '])).toStrictEqual([{ text: '正文', hit: false }])
    expect(splitByHits('正文', null)).toStrictEqual([{ text: '正文', hit: false }])
    expect(splitByHits('', ['x'])).toStrictEqual([])
  })

  test('命中次数有上限', () => {
    const segs = splitByHits('a'.repeat(1000), ['a'], 5)
    expect(segs.filter((s) => s.hit)).toHaveLength(1)
    expect(segs.map((s) => s.text).join('')).toHaveLength(1000)
  })
})

describe('clipForDisplay', () => {
  test('超长只截断显示', () => {
    expect(clipForDisplay('abc', 10)).toStrictEqual({ text: 'abc', clipped: false })
    expect(clipForDisplay('abcdef', 3)).toStrictEqual({ text: 'abc', clipped: true })
  })
})

describe('formatBytes', () => {
  test('B / KB / MB', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(null)).toBe('0 B')
    expect(formatBytes(2048)).toBe('2.0 KB')
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB')
  })
})

describe('meta 行', () => {
  test('来源站条目：缺失字段省略，时长不带单位', () => {
    const item = {
      moduleAge: '现代',
      structure: '线性',
      minAmount: 2,
      maxAmount: 4,
      minDuration: 3,
      maxDuration: 3,
      moduleVersion: 'v1.2',
      occurrencePlace: '上海',
    } as BrowseItem
    expect(cnmodsMetaLine(item)).toBe('现代 · 线性 · 2-4人 · 时长 3 · 上海 · v1.2')
    expect(cnmodsMetaLine({ minAmount: 0, maxAmount: 0 } as BrowseItem)).toBe('')
    expect(cnmodsMetaLine({ minAmount: 3, maxAmount: 0 } as BrowseItem)).toBe('3人')
  })

  test('本地条目 meta 与来源站条目格式一致', () => {
    expect(moduleMetaLine({ module_age: '现代', min_amount: 1, max_amount: 3, version: '2' })).toBe('现代 · 1-3人 · v2')
    expect(moduleMetaLine(null)).toBe('')
    expect(moduleMetaLine({})).toBe('')
  })
})

describe('cnmodsByline', () => {
  test('作者与日期，缺失则省略，日期只取日', () => {
    expect(cnmodsByline({ article: '某某', releaseDate: '2024-05-06 12:00:00' })).toBe('作者 某某 · 2024-05-06')
    expect(cnmodsByline({ article: '', releaseDate: '2024-05-06' })).toBe('2024-05-06')
    expect(cnmodsByline({ article: '某某', releaseDate: '' })).toBe('作者 某某')
  })
})

describe('toCnmodsItem', () => {
  test('去掉浏览接口附加的 local，其余字段原样', () => {
    const item = { keyId: '9', title: 't', url: 'https://x', local: { id: 1, status: 'failed', scenario_id: null } } as BrowseItem
    const out = toCnmodsItem(item)
    expect(out).toStrictEqual({ keyId: '9', title: 't', url: 'https://x' })
    expect('local' in out).toBe(false)
    expect(item.local).not.toBeNull()
  })
})

describe('safeUrl', () => {
  test('只放行 http(s)', () => {
    expect(safeUrl('https://x.lanzoul.com/abc')).toBe('https://x.lanzoul.com/abc')
    expect(safeUrl(' http://a.b ')).toBe('http://a.b')
    expect(safeUrl('javascript:alert(1)')).toBe('')
    expect(safeUrl('//a.b')).toBe('')
    expect(safeUrl(null)).toBe('')
  })
})

describe('validateArchiveFile', () => {
  test('扩展名、空文件与体积上限', () => {
    expect(validateArchiveFile({ name: 'a.ZIP', size: 10 })).toBeNull()
    expect(validateArchiveFile({ name: 'a.docx', size: 10 })).toBeNull()
    expect(validateArchiveFile({ name: 'a.pdf', size: 10 })).toBeNull()
    expect(validateArchiveFile({ name: 'a.rar', size: 10 })).toBe('仅支持 .zip / .docx / .pdf 格式')
    expect(validateArchiveFile({ name: 'a.zip', size: 0 })).toBe('文件内容为空')
    expect(validateArchiveFile({ name: 'a.zip', size: 50 * 1024 * 1024 + 1 })).toBe('文件过大（最大 50MB）')
  })
})

describe('stageLabel', () => {
  test('四个阶段有中文名；空值返回空串；未知阶段原样显示', () => {
    expect(stageLabel('resolve')).toBe('解析分享页')
    expect(stageLabel('download')).toBe('下载压缩包')
    expect(stageLabel('extract')).toBe('提取正文')
    expect(stageLabel('ocr')).toBe('OCR 识别')
    expect(stageLabel('')).toBe('')
    expect(stageLabel(null)).toBe('')
    expect(stageLabel('future')).toBe('future')
  })
})

describe('formatElapsed', () => {
  test('不足一分钟只写秒，否则「N分SS秒」，秒补零', () => {
    expect(formatElapsed(0)).toBe('0秒')
    expect(formatElapsed(999)).toBe('0秒')
    expect(formatElapsed(59_999)).toBe('59秒')
    expect(formatElapsed(60_000)).toBe('1分00秒')
    expect(formatElapsed(125_000)).toBe('2分05秒')
    expect(formatElapsed(12 * 60_000 + 30_000)).toBe('12分30秒')
  })

  test('负数、NaN 按 0 处理', () => {
    expect(formatElapsed(-5000)).toBe('0秒')
    expect(formatElapsed(Number.NaN)).toBe('0秒')
  })
})

describe('taskElapsedMs', () => {
  const now = Date.parse('2026-11-14T08:10:00Z')

  test('从 task_started_at 算起；缺失或非法返回 null；时钟偏差不出现负数', () => {
    expect(taskElapsedMs({ task_started_at: '2026-11-14T08:07:55Z' }, now)).toBe(125_000)
    expect(taskElapsedMs({ task_started_at: null }, now)).toBeNull()
    expect(taskElapsedMs({ task_started_at: 'garbage' }, now)).toBeNull()
    expect(taskElapsedMs({ task_started_at: '2026-11-14T08:20:00Z' }, now)).toBe(0)
  })
})

describe('sortTaskModules', () => {
  const m = (id: number, status: OnlineStatus, updated: string, started: string | null = null) => ({
    id,
    status,
    updated_at: updated,
    task_started_at: started,
  })

  test('下载中的在前且开始得晚的更靠前；其余按更新时间倒序；不修改入参', () => {
    const input = [
      m(1, 'extracted', '2026-11-14T08:00:00Z'),
      m(2, 'downloading', '2026-11-14T07:00:00Z', '2026-11-14T07:00:00Z'),
      m(3, 'failed', '2026-11-14T09:00:00Z'),
      m(4, 'downloading', '2026-11-14T06:00:00Z', '2026-11-14T07:30:00Z'),
    ]
    const copy = [...input]
    expect(sortTaskModules(input).map((x) => x.id)).toStrictEqual([4, 2, 3, 1])
    expect(input).toStrictEqual(copy)
  })

  test('更新时间相同按 id 倒序，结果稳定；时间非法排最后', () => {
    const out = sortTaskModules([
      m(1, 'extracted', '2026-11-14T08:00:00Z'),
      m(2, 'extracted', '2026-11-14T08:00:00Z'),
      m(3, 'extracted', 'bad'),
    ])
    expect(out.map((x) => x.id)).toStrictEqual([2, 1, 3])
  })
})
