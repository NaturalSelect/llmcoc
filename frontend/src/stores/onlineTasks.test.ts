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
