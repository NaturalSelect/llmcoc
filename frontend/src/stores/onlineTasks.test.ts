// @vitest-environment jsdom
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import type { OnlineModule } from '@/api/adminTypesOnline'
import { FakeWebSocket } from '@/testing/fakeWebSocket'
import { useOnlineTasksStore } from './onlineTasks'
import type { ModuleChange } from './onlineTasks'
import { useUiStore } from './ui'

const mod = (id: number, over: Partial<OnlineModule> = {}): OnlineModule =>
  ({
    id,
    title: '模组' + id,
    status: 'extracted',
    error: '',
    stage: '',
    stage_note: '',
    task_started_at: null,
    scenario_id: null,
    ...over,
  }) as OnlineModule

const downloading = (id: number) => mod(id, { status: 'downloading', stage: 'download', task_started_at: '2026-11-14T08:00:00Z' })

const toasts = () => useUiStore().toasts.map((t) => [t.message, t.type])

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.setItem('coc_token', 't')
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  FakeWebSocket.install()
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => ({}) }) as Response))
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  localStorage.clear()
})

describe('onlineTasks store：连接的引用计数', () => {
  test('第一个页面 acquire 才打开连接，最后一个 release 才关闭；多次 acquire 共用一个连接', () => {
    const s = useOnlineTasksStore()
    expect(FakeWebSocket.instances).toHaveLength(0)
    s.acquire()
    s.acquire()
    expect(FakeWebSocket.instances).toHaveLength(1)

    s.release()
    expect(FakeWebSocket.last.closed).toBe(false)
    s.release()
    expect(FakeWebSocket.last.closed).toBe(true)
  })

  test('多余的 release 不会让计数变负：之后 acquire 仍然只开一个连接', () => {
    const s = useOnlineTasksStore()
    s.release()
    s.acquire()
    expect(FakeWebSocket.instances).toHaveLength(1)
    s.release()
    expect(FakeWebSocket.last.closed).toBe(true)
  })

  test('关闭后清空状态；重新打开会重新开始：新的首帧快照仍然只是基线、不弹提示', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [downloading(1)] } })
    expect(s.loaded).toBe(true)
    expect(s.connected).toBe(true)
    s.release()
    expect(s.modules).toStrictEqual({})
    expect(s.loaded).toBe(false)
    expect(s.connected).toBe(false)

    s.acquire()
    expect(FakeWebSocket.instances).toHaveLength(2)
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [mod(1)] } })
    expect(toasts()).toStrictEqual([])
  })

  test('关闭连接后，旧连接的迟到帧不会写进状态', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    const first = FakeWebSocket.last
    s.release()
    first.recv({ type: 'snapshot', data: { modules: [mod(1)] } })
    expect(s.modules).toStrictEqual({})
  })
})

describe('onlineTasks store：帧归约', () => {
  test('快照整体替换，module 按 id 覆盖，deleted 移除；connected / loaded 随快照置位', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    expect(s.loaded).toBe(false)
    expect(s.connected).toBe(false)

    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [mod(1), mod(2)] } })
    expect(Object.keys(s.modules)).toStrictEqual(['1', '2'])
    expect(s.loaded).toBe(true)
    expect(s.connected).toBe(true)

    FakeWebSocket.last.recv({ type: 'module', data: mod(2, { title: '改名了' }) })
    FakeWebSocket.last.recv({ type: 'module', data: mod(3) })
    expect(s.modules[2]!.title).toBe('改名了')
    expect(Object.keys(s.modules)).toStrictEqual(['1', '2', '3'])

    FakeWebSocket.last.recv({ type: 'deleted', data: { id: 1 } })
    expect(Object.keys(s.modules)).toStrictEqual(['2', '3'])

    // 重连后的快照替换整张表：不在快照里的条目消失
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [mod(9)] } })
    expect(Object.keys(s.modules)).toStrictEqual(['9'])
  })

  test('断线后 connected 回到 false，已有数据保留；重连的快照到达再置回 true', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [mod(1)] } })
    FakeWebSocket.last.drop()
    expect(s.connected).toBe(false)
    expect(s.loaded).toBe(true)
    expect(s.modules[1]).toBeDefined()

    vi.advanceTimersByTime(1000)
    expect(FakeWebSocket.instances).toHaveLength(2)
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [mod(1)] } })
    expect(s.connected).toBe(true)
  })
})

describe('onlineTasks store：任务结束提示', () => {
  test('downloading → 非 downloading 且 error 为空：成功提示一次', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [downloading(1)] } })
    expect(toasts()).toStrictEqual([])

    FakeWebSocket.last.recv({ type: 'module', data: mod(1, { status: 'extracted', title: '雨夜档案' }) })
    expect(toasts()).toStrictEqual([['已提取正文：雨夜档案', 'success']])

    // 之后同一模组的其他变化（放行、编译）不再提示
    FakeWebSocket.last.recv({ type: 'module', data: mod(1, { status: 'extracted', title: '雨夜档案', scenario_id: 5 }) })
    expect(toasts()).toHaveLength(1)
  })

  test('结束时 error 非空：以「标题：原因」弹错误提示，状态是 failed 或恢复后的 extracted 都一样', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [downloading(1), downloading(2)] } })
    FakeWebSocket.last.recv({ type: 'module', data: mod(1, { status: 'failed', title: 'A', error: '下载被拦截' }) })
    FakeWebSocket.last.recv({ type: 'module', data: mod(2, { status: 'extracted', title: 'B', error: '压缩包损坏' }) })
    expect(toasts()).toStrictEqual([
      ['A：下载被拦截', 'error'],
      ['B：压缩包损坏', 'error'],
    ])
  })

  test('第一次快照不提示：即使快照里的模组已经带着错误或刚结束', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({
      type: 'snapshot',
      data: { modules: [mod(1, { status: 'failed', error: '旧的失败' }), mod(2)] },
    })
    expect(toasts()).toStrictEqual([])
  })

  test('没见过的模组直接以非 downloading 到达：不提示（没有「之前是下载中」的依据）', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [] } })
    FakeWebSocket.last.recv({ type: 'module', data: mod(7, { status: 'failed', error: 'x' }) })
    expect(toasts()).toStrictEqual([])
  })

  test('重连后的快照里某模组已结束：补一次提示（断线期间错过了结束帧）', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [downloading(1)] } })
    FakeWebSocket.last.drop()
    vi.advanceTimersByTime(1000)
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [mod(1, { title: '雨夜档案' })] } })
    expect(toasts()).toStrictEqual([['已提取正文：雨夜档案', 'success']])
  })

  test('仍在下载中的更新（阶段变化）不提示', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [downloading(1)] } })
    FakeWebSocket.last.recv({ type: 'module', data: { ...downloading(1), stage: 'ocr', stage_note: 'a.pdf' } })
    expect(toasts()).toStrictEqual([])
    expect(s.modules[1]!.stage).toBe('ocr')
  })
})

describe('onlineTasks store：onChange 订阅', () => {
  test('每个 module 帧和快照里的每个模组都通知订阅者，并带 settled 标记；取消订阅后不再通知', () => {
    const s = useOnlineTasksStore()
    const seen: Array<[number, boolean]> = []
    const off = s.onChange((c: ModuleChange) => seen.push([c.module.id, c.settled]))
    s.acquire()

    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [downloading(1), mod(2)] } })
    FakeWebSocket.last.recv({ type: 'module', data: mod(1) })
    FakeWebSocket.last.recv({ type: 'module', data: mod(2, { scenario_id: 3 }) })
    expect(seen).toStrictEqual([
      [1, false],
      [2, false],
      [1, true],
      [2, false],
    ])

    off()
    FakeWebSocket.last.recv({ type: 'module', data: mod(3) })
    expect(seen).toHaveLength(4)
  })

  test('订阅者里取消订阅不影响本轮其余订阅者', () => {
    const s = useOnlineTasksStore()
    const calls: string[] = []
    const offA = s.onChange(() => {
      calls.push('a')
      offA()
    })
    s.onChange(() => calls.push('b'))
    s.acquire()
    FakeWebSocket.last.recv({ type: 'module', data: mod(1) })
    FakeWebSocket.last.recv({ type: 'module', data: mod(1) })
    expect(calls).toStrictEqual(['a', 'b', 'b'])
  })
})

// NOTE: 编译日志：服务端按绝对行号推 log 帧，GET compile-log 返回 {base, lines}；两路数据按行号合并，到达顺序不定
describe('onlineTasks store：编译日志', () => {
  const compiling = (id: number) => mod(id, { status: 'downloading', stage: 'compile', task_started_at: '2026-11-14T08:00:00Z' })
  const logFrame = (id: number, index: number, line = `L${index}`) => FakeWebSocket.last.recv({ type: 'log', data: { id, index, line } })

  let logBodies: Record<number, unknown>
  let logGets: number[]
  // NOTE: gated 为真时，之后发出的 compile-log 请求先按「发出那一刻」的内容定下响应，再等 releaseAll() 才返回，
  // 用来构造「GET 比 log 帧晚到」和「GET 在快照之后才返回」
  let gated: boolean
  let pending: Array<() => void>
  const releaseAll = () => {
    const list = pending
    pending = []
    for (const release of list) release()
  }

  beforeEach(() => {
    logBodies = {}
    logGets = []
    gated = false
    pending = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: string) => {
        const m = /\/online-modules\/(\d+)\/compile-log$/.exec(path)
        if (!m) return { ok: true, status: 200, json: async () => ({}) } as Response
        const id = Number(m[1])
        logGets.push(id)
        const body = logBodies[id] ?? { base: 0, lines: [] }
        if (body === 'fail') return { ok: false, status: 500, json: async () => ({ error: '炸了' }) } as Response
        if (gated) await new Promise<void>((res) => pending.push(res))
        return { ok: true, status: 200, json: async () => body } as Response
      }),
    )
  })

  const settle = async () => {
    for (let i = 0; i < 20; i++) await Promise.resolve()
  }

  test('log 帧按行号写入；同一行号重复到达没有副作用；没有日志的模组返回空数组', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    expect(s.compileLines(1)).toStrictEqual([])
    logFrame(1, 0)
    logFrame(1, 1)
    logFrame(1, 1, 'L1')
    logFrame(1, 0)
    expect(s.compileLines(1)).toStrictEqual(['L0', 'L1'])
    expect(s.compileLines(2)).toStrictEqual([])
  })

  test('只返回连续尾部：从最大行号往下数，遇到缺口就停，缺口以下的行不显示', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    for (const i of [0, 1, 3, 4]) logFrame(1, i)
    expect(s.compileLines(1)).toStrictEqual(['L3', 'L4'])
    // 补上缺的一行后整段连起来
    logFrame(1, 2)
    expect(s.compileLines(1)).toStrictEqual(['L0', 'L1', 'L2', 'L3', 'L4'])
  })

  test('乱序合并：实时帧先到、GET 后到——先显示帧里的尾部，GET 补齐前面的行（GET 与帧重叠的行不重复）', async () => {
    const s = useOnlineTasksStore()
    s.acquire()
    gated = true
    logBodies[1] = { base: 0, lines: ['L0', 'L1', 'L2', 'L3', 'L4', 'L5'] }
    // 快照触发的 GET 还没返回，此时实时帧先到
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [compiling(1)] } })
    logFrame(1, 5)
    logFrame(1, 6)
    expect(s.compileLines(1)).toStrictEqual(['L5', 'L6'])

    releaseAll()
    await settle()
    expect(s.compileLines(1)).toStrictEqual(['L0', 'L1', 'L2', 'L3', 'L4', 'L5', 'L6'])
  })

  test('loadCompileLog：lines[i] 写到 base + i；base 大于 0（服务端已丢掉最旧的行）时只显示 base 之后的行', async () => {
    const s = useOnlineTasksStore()
    logBodies[3] = { base: 40, lines: ['a', 'b', 'c'] }
    expect(await s.loadCompileLog(3)).toBe(true)
    expect(s.compileLines(3)).toStrictEqual(['a', 'b', 'c'])
    // 之后的实时帧接在 base + len 上
    s.acquire()
    logFrame(3, 43, 'd')
    expect(s.compileLines(3)).toStrictEqual(['a', 'b', 'c', 'd'])
  })

  test('loadCompileLog 失败（HTTP 错误、网络错误、响应畸形）不抛异常，返回 false，已有日志不受影响', async () => {
    const s = useOnlineTasksStore()
    s.acquire()
    logFrame(1, 0)
    logBodies[1] = 'fail'
    expect(await s.loadCompileLog(1)).toBe(false)

    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('offline'))))
    expect(await s.loadCompileLog(1)).toBe(false)

    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ base: 'x', lines: 5 }) }) as Response))
    expect(await s.loadCompileLog(1)).toBe(false)
    expect(s.compileLines(1)).toStrictEqual(['L0'])
  })

  test('每个模组最多保留 1000 行：超出时丢掉行号最小的', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    for (let i = 0; i < 1205; i++) logFrame(1, i)
    const lines = s.compileLines(1)
    expect(lines).toHaveLength(1000)
    expect(lines[0]).toBe('L205')
    expect(lines.at(-1)).toBe('L1204')
    // 已经滑出窗口的迟到旧行不会把它写回来
    logFrame(1, 3)
    expect(s.compileLines(1)).toHaveLength(1000)
    expect(s.compileLines(1)[0]).toBe('L205')
  })

  test('deleted 帧丢掉该模组的日志，其他模组不受影响', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    logFrame(1, 0)
    logFrame(2, 0)
    FakeWebSocket.last.recv({ type: 'deleted', data: { id: 1 } })
    expect(s.compileLines(1)).toStrictEqual([])
    expect(s.compileLines(2)).toStrictEqual(['L0'])
  })

  test('快照（含重连）清空所有日志，并为「有过日志」和「正在编译」的模组重新拉取，其余模组不拉', async () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [mod(1), mod(2), mod(3)] } })
    await settle()
    expect(logGets).toStrictEqual([])

    logFrame(1, 7, '旧日志')
    expect(s.compileLines(1)).toStrictEqual(['旧日志'])

    // 服务端重启过：模组 1 的日志没了，模组 2 正在编译并且已有新日志
    logBodies[1] = { base: 0, lines: [] }
    logBodies[2] = { base: 0, lines: ['x', 'y'] }
    FakeWebSocket.last.drop()
    vi.advanceTimersByTime(1000)
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [mod(1), compiling(2), mod(3)] } })
    // 清空是同步的，旧行不会在 GET 返回前继续显示
    expect(s.compileLines(1)).toStrictEqual([])
    await settle()
    expect([...logGets].sort()).toStrictEqual([1, 2])
    expect(s.compileLines(1)).toStrictEqual([])
    expect(s.compileLines(2)).toStrictEqual(['x', 'y'])
    expect(s.compileLines(3)).toStrictEqual([])
  })

  test('页面刷新后的首帧快照里有正在编译的模组：自动拉取日志；之后的实时帧接在后面', async () => {
    const s = useOnlineTasksStore()
    logBodies[4] = { base: 0, lines: ['a', 'b'] }
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [compiling(4)] } })
    await settle()
    expect(s.compileLines(4)).toStrictEqual(['a', 'b'])
    logFrame(4, 2, 'c')
    expect(s.compileLines(4)).toStrictEqual(['a', 'b', 'c'])
  })

  test('快照之前发出、快照之后才返回的旧响应被丢弃，不会把重启前的行写回来；快照会为在途的拉取重新发起', async () => {
    const s = useOnlineTasksStore()
    s.acquire()
    gated = true
    logBodies[1] = { base: 0, lines: ['重启前的旧行', '更旧'] }
    const first = s.loadCompileLog(1)
    await settle()
    expect(logGets).toStrictEqual([1])

    logBodies[1] = { base: 0, lines: ['新日志'] }
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [mod(1)] } })
    await settle()
    // 在途的拉取被快照重新发起了一次
    expect(logGets).toStrictEqual([1, 1])

    releaseAll()
    expect(await first).toBe(false)
    await settle()
    expect(s.compileLines(1)).toStrictEqual(['新日志'])
  })

  test('release 关闭连接后日志一并清空', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    logFrame(1, 0)
    s.release()
    expect(s.compileLines(1)).toStrictEqual([])
  })
})

describe('onlineTasks store：编译结束提示与 upsertModule', () => {
  const running = (over: Partial<OnlineModule> = {}) =>
    mod(1, { status: 'downloading', stage: 'compile', title: '雨夜档案', updated_at: '2026-11-14T08:00:00Z', ...over })

  test('编译结束：成功提示「编译完成」而不是「已提取正文」；失败提示带原因', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [running(), running({ id: 2, title: 'B' })] } })
    FakeWebSocket.last.recv({ type: 'module', data: mod(1, { title: '雨夜档案', scenario_id: 9 }) })
    FakeWebSocket.last.recv({ type: 'module', data: mod(2, { title: 'B', error: '模组编译失败: x' }) })
    expect(toasts()).toStrictEqual([
      ['编译完成，已入库：雨夜档案', 'success'],
      ['B：模组编译失败: x', 'error'],
    ])
  })

  test('upsertModule：202 的 downloading 记录写进 store，并按 module 帧同样的方式通知订阅者', () => {
    const s = useOnlineTasksStore()
    const seen: number[] = []
    s.onChange((c) => seen.push(c.module.id))
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [mod(1)] } })
    expect(s.upsertModule(running({ updated_at: '2026-11-14T09:00:00Z' }))).toBe(true)
    expect(s.modules[1]!.stage).toBe('compile')
    expect(seen).toStrictEqual([1, 1])
  })

  test('upsertModule：store 里已有更新的结束状态时拒绝套用 202（不会退回进行中），并补一次结果提示；已提示过则不重复', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [mod(1, { title: '雨夜档案', updated_at: '2026-11-14T08:00:00Z' })] } })
    // 结束帧先于 202 到达，且 store 没见过 downloading 帧
    FakeWebSocket.last.recv({ type: 'module', data: mod(1, { title: '雨夜档案', scenario_id: 9, updated_at: '2026-11-14T09:00:05Z' }) })
    expect(toasts()).toStrictEqual([])

    expect(s.upsertModule(running({ updated_at: '2026-11-14T09:00:00Z' }))).toBe(false)
    expect(s.modules[1]!.status).toBe('extracted')
    expect(toasts()).toStrictEqual([['编译完成，已入库：雨夜档案', 'success']])
    // 同一个结束状态不会再提示第二次
    expect(s.upsertModule(running({ updated_at: '2026-11-14T09:00:00Z' }))).toBe(false)
    expect(toasts()).toHaveLength(1)
  })

  test('upsertModule：store 已经见过 downloading → 结束的完整过程时，结果提示只有帧触发的那一次', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [] } })
    FakeWebSocket.last.recv({ type: 'module', data: running({ updated_at: '2026-11-14T09:00:00Z' }) })
    FakeWebSocket.last.recv({ type: 'module', data: mod(1, { title: '雨夜档案', scenario_id: 9, updated_at: '2026-11-14T09:00:05Z' }) })
    expect(toasts()).toHaveLength(1)
    expect(s.upsertModule(running({ updated_at: '2026-11-14T09:00:00Z' }))).toBe(false)
    expect(toasts()).toHaveLength(1)
  })

  test('upsertModule：store 里是进行中且更新（阶段备注已推进）时不采用 202，也不提示', () => {
    const s = useOnlineTasksStore()
    s.acquire()
    FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [running({ stage_note: '第 3 步', updated_at: '2026-11-14T09:00:03Z' })] } })
    expect(s.upsertModule(running({ updated_at: '2026-11-14T09:00:00Z' }))).toBe(false)
    expect(s.modules[1]!.stage_note).toBe('第 3 步')
    expect(toasts()).toStrictEqual([])
  })
})
