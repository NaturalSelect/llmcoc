import { describe, expect, test } from 'vitest'
import type { BatchDone } from '@/api/adminTypesContent'
import {
  createCompileHandler,
  createGenerationHandler,
  formatLogLine,
  progressDataLine,
  progressLine,
  summarizeBatchDone,
  summarizeCompile,
  validateStoryFile,
} from './scenarioStream'

function runGeneration(events: Array<[string, string]>) {
  const logs: string[] = []
  const h = createGenerationHandler((l) => logs.push(l))
  for (const [e, d] of events) h.onEvent(e, d)
  return { logs, handler: h }
}

describe('progressLine', () => {
  test('exchange 事件展示 LLM 交互摘要', () => {
    expect(progressLine({ stage: 'exchange', status: 'ok', detail: 'architect → 1200 tokens' })).toBe('· LLM 交互 [ok] architect → 1200 tokens')
    expect(progressLine({ stage: 'exchange' })).toBe('· LLM 交互 [] ')
  })

  test('其他事件优先 detail，缺 detail 时回落到 stage + status', () => {
    expect(progressLine({ stage: 'queued', status: 'start', detail: '请求已受理' })).toBe('请求已受理')
    expect(progressLine({ stage: 'repair', status: 'done' })).toBe('repair done')
    expect(progressLine({ stage: 'repair' })).toBe('repair ')
  })

  test('非对象按字符串输出，空值为空串', () => {
    expect(progressLine('纯文本')).toBe('纯文本')
    expect(progressLine(null)).toBe('')
    expect(progressLine(0)).toBe('')
  })

  test('progressDataLine：非法 JSON 按原文显示', () => {
    expect(progressDataLine('{"stage":"x","status":"y","detail":"细节"}')).toBe('细节')
    expect(progressDataLine('not json')).toBe('not json')
  })
})

describe('formatLogLine', () => {
  test('加 24 小时制时间前缀', () => {
    const line = formatLogLine('hello', new Date(2026, 0, 1, 9, 5, 7))
    expect(line).toMatch(/^\[\d{2}:\d{2}:\d{2}\] hello$/)
  })
})

describe('createGenerationHandler', () => {
  test('各事件的日志文案', () => {
    const { logs } = runGeneration([
      ['progress', '{"stage":"queued","status":"start","detail":"排队中"}'],
      ['batch_progress', '{"current":2,"total":3}'],
      ['done', '{"index":2,"name":"雨夜"}'],
      ['error', '{"index":3,"message":"模组生成失败: boom"}'],
    ])
    expect(logs).toStrictEqual([
      '排队中',
      '📦 开始生成第 2/3 个模组…',
      '✅ 第 2 个生成完成：《雨夜》，已入库',
      '❌ 第 3 个生成失败：模组生成失败: boom',
    ])
  })

  test('done 缺 index / name 时用默认值；error 缺 message 时用默认文案', () => {
    const { logs } = runGeneration([
      ['done', '{}'],
      ['error', '{}'],
    ])
    expect(logs).toStrictEqual(['✅ 第 1 个生成完成：《未命名》，已入库', '❌ 第 1 个生成失败：生成失败'])
  })

  test('畸形 JSON：batch_progress / done 静默忽略，error 按原文，progress 按原文', () => {
    const { logs } = runGeneration([
      ['batch_progress', 'oops'],
      ['done', 'oops'],
      ['error', 'oops'],
      ['error', ''],
      ['progress', 'oops'],
    ])
    expect(logs).toStrictEqual(['❌ oops', '❌ 生成失败', 'oops'])
  })

  test('未知事件被忽略；batch_done 被记录，畸形 batch_done 清空', () => {
    const { logs, handler } = runGeneration([
      ['ping', 'x'],
      ['batch_done', '{"total":1,"succeeded":1,"failed":0}'],
    ])
    expect(logs).toStrictEqual([])
    expect(handler.batchDone).toStrictEqual({ total: 1, succeeded: 1, failed: 0 })
    handler.onEvent('batch_done', 'oops')
    expect(handler.batchDone).toBeNull()
  })
})

describe('summarizeBatchDone', () => {
  const done = (p: Partial<BatchDone>): BatchDone => ({ total: 1, succeeded: 1, failed: 0, ...p })

  test('没有 batch_done：连接中断提示，不刷新列表', () => {
    expect(summarizeBatchDone(null)).toStrictEqual({
      logs: ['⚠️ 连接中断，生成仍在后台继续，稍后请刷新模组列表'],
      toast: { message: '连接中断，生成仍在后台继续', type: 'error' },
      reload: false,
    })
  })

  test('单个成功', () => {
    expect(summarizeBatchDone(done({}))).toStrictEqual({
      logs: [],
      toast: { message: 'AI 模组生成并入库成功', type: 'success' },
      reload: true,
    })
  })

  test('单个失败：取第一条结果的错误，缺省为「生成失败」；不刷新', () => {
    expect(summarizeBatchDone(done({ succeeded: 0, failed: 1, results: [{ index: 1, error: '模组入库失败' }] }))).toStrictEqual({
      logs: [],
      toast: { message: '模组入库失败', type: 'error' },
      reload: false,
    })
    expect(summarizeBatchDone(done({ succeeded: 0, failed: 1 })).toast).toStrictEqual({ message: '生成失败', type: 'error' })
  })

  test('批量：全部成功为 success，有失败为 error，日志与提示文案不同', () => {
    expect(summarizeBatchDone(done({ total: 3, succeeded: 3, failed: 0 }))).toStrictEqual({
      logs: ['🏁 批量生成完成：成功 3 个，失败 0 个'],
      toast: { message: '批量生成完成：成功 3 个', type: 'success' },
      reload: true,
    })
    expect(summarizeBatchDone(done({ total: 3, succeeded: 2, failed: 1 }))).toStrictEqual({
      logs: ['🏁 批量生成完成：成功 2 个，失败 1 个'],
      toast: { message: '批量生成完成：成功 2 个，失败 1 个', type: 'error' },
      reload: true,
    })
  })

  test('批量全部失败：提示 error 且不刷新', () => {
    const o = summarizeBatchDone(done({ total: 2, succeeded: 0, failed: 2 }))
    expect(o.toast.type).toBe('error')
    expect(o.reload).toBe(false)
  })
})

describe('compile handler 与 summarizeCompile', () => {
  function runCompile(events: Array<[string, string]>) {
    const logs: string[] = []
    const h = createCompileHandler((l) => logs.push(l))
    for (const [e, d] of events) h.onEvent(e, d)
    return { logs, h }
  }

  test('progress 写日志，done / error 只记录不写日志', () => {
    const { logs, h } = runCompile([
      ['progress', '{"stage":"compile","status":"start","detail":"编译中"}'],
      ['done', '{"name":"雨夜"}'],
    ])
    expect(logs).toStrictEqual(['编译中'])
    expect(h.done).toStrictEqual({ name: '雨夜' })
    expect(h.error).toBeNull()
  })

  test('error 事件：JSON 取 message，畸形时用原文，空数据用「编译失败」', () => {
    expect(runCompile([['error', '{"index":1,"message":"模组编译失败: x"}']]).h.error).toStrictEqual({ index: 1, message: '模组编译失败: x' })
    expect(runCompile([['error', 'raw']]).h.error).toStrictEqual({ message: 'raw' })
    expect(runCompile([['error', '']]).h.error).toStrictEqual({ message: '编译失败' })
  })

  test('畸形 done 视为没有 done', () => {
    expect(runCompile([['done', 'oops']]).h.done).toBeNull()
  })

  test('summarizeCompile：done 优先于 error，其次 error，最后是连接中断', () => {
    expect(summarizeCompile({ name: '雨夜' }, { message: 'x' })).toStrictEqual({
      logs: ['✅ 编译完成：《雨夜》，已入库'],
      toast: { message: '故事编译并入库成功', type: 'success' },
      reload: true,
    })
    expect(summarizeCompile({}, null).logs).toStrictEqual(['✅ 编译完成：《未命名》，已入库'])
    expect(summarizeCompile(null, { message: '模组编译失败: x' })).toStrictEqual({
      logs: ['❌ 模组编译失败: x'],
      toast: { message: '模组编译失败: x', type: 'error' },
      reload: false,
    })
    expect(summarizeCompile(null, {}).toast.message).toBe('编译失败')
    expect(summarizeCompile(null, null)).toStrictEqual({
      logs: ['⚠️ 连接中断，编译仍在后台继续，稍后请刷新模组列表'],
      toast: { message: '连接中断，编译仍在后台继续', type: 'error' },
      reload: false,
    })
  })
})

describe('validateStoryFile', () => {
  test('扩展名不区分大小写，支持 md / markdown / txt', () => {
    for (const name of ['a.md', 'a.MD', 'a.markdown', 'a.txt', '雨夜.Txt']) {
      expect(validateStoryFile({ name, size: 10 })).toBeNull()
    }
  })

  test('其他扩展名被拒；扩展名检查先于大小检查', () => {
    expect(validateStoryFile({ name: 'a.docx', size: 10 })).toBe('仅支持 .md / .markdown / .txt 格式')
    expect(validateStoryFile({ name: 'a.docx', size: 10 * 1024 * 1024 })).toBe('仅支持 .md / .markdown / .txt 格式')
  })

  test('大小上限 512KB：恰好等于上限通过，超过一字节被拒', () => {
    expect(validateStoryFile({ name: 'a.md', size: 512 * 1024 })).toBeNull()
    expect(validateStoryFile({ name: 'a.md', size: 512 * 1024 + 1 })).toBe('文件过大（最大 500KB）')
  })
})
