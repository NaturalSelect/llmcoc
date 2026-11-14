// @vitest-environment jsdom
// NOTE: 用假的 WebSocket 和 vitest 假定时器驱动 OnlineTaskSocket 的连接、退避、探测和帧解析；store 与页面的行为在各自的测试里
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import type { OnlineModule } from '@/api/adminTypesOnline'
import { ApiError } from '@/api/client'
import { FakeWebSocket } from '@/testing/fakeWebSocket'
import { OnlineTaskSocket, parseOnlineTaskFrame } from './onlineTaskSocket'

const mod = (id: number, over: Partial<OnlineModule> = {}): OnlineModule =>
  ({ id, title: 'm' + id, status: 'extracted', error: '', stage: '', stage_note: '', task_started_at: null, ...over }) as OnlineModule

const flush = async () => {
  for (let i = 0; i < 20; i++) await Promise.resolve()
}

function harness(over: { api?: () => Promise<unknown>; token?: string } = {}) {
  const sockets: FakeWebSocket[] = []
  const events: string[] = []
  const probes: string[] = []
  const deps = {
    onSnapshot: vi.fn((list: OnlineModule[]) => events.push('snapshot:' + list.map((m) => m.id).join(','))),
    onModule: vi.fn((m: OnlineModule) => events.push('module:' + m.id)),
    onDeleted: vi.fn((id: number) => events.push('deleted:' + id)),
    onStatus: vi.fn((v: boolean) => events.push('status:' + v)),
    token: () => over.token ?? 'tok en',
    api: async (method: string, path: string) => {
      probes.push(method + ' ' + path)
      return over.api ? over.api() : {}
    },
    createSocket: (url: string) => {
      const ws = new FakeWebSocket(url)
      sockets.push(ws)
      return ws as unknown as WebSocket
    },
  }
  const ctl = new OnlineTaskSocket(deps)
  return { ctl, deps, sockets, events, probes, ws: () => sockets[sockets.length - 1]! }
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  FakeWebSocket.instances = []
})

afterEach(() => {
  vi.useRealTimers()
})

describe('parseOnlineTaskFrame', () => {
  test('三种合法帧', () => {
    expect(parseOnlineTaskFrame(JSON.stringify({ type: 'snapshot', data: { modules: [mod(1)] } }))?.type).toBe('snapshot')
    expect(parseOnlineTaskFrame(JSON.stringify({ type: 'module', data: mod(2) }))?.type).toBe('module')
    expect(parseOnlineTaskFrame(JSON.stringify({ type: 'deleted', data: { id: 3 } }))).toStrictEqual({ type: 'deleted', data: { id: 3 } })
  })

  test('畸形 JSON、非对象、未知类型、缺字段一律返回 null', () => {
    for (const raw of ['{', 'null', '42', '"x"', '[]', '{}', '{"type":"future","data":{}}', '{"type":"module","data":null}']) {
      expect(parseOnlineTaskFrame(raw)).toBeNull()
    }
    expect(parseOnlineTaskFrame(new ArrayBuffer(2))).toBeNull()
    expect(parseOnlineTaskFrame('{"type":"module","data":{"title":"没有 id"}}')).toBeNull()
    expect(parseOnlineTaskFrame('{"type":"deleted","data":{"id":"7"}}')).toBeNull()
    expect(parseOnlineTaskFrame('{"type":"snapshot","data":{}}')).toBeNull()
  })

  test('快照里没有 id 的条目被滤掉，其余保留', () => {
    const f = parseOnlineTaskFrame(JSON.stringify({ type: 'snapshot', data: { modules: [mod(1), null, { title: 'x' }, mod(2)] } }))
    expect(f?.type === 'snapshot' && f.data.modules.map((m) => m.id)).toStrictEqual([1, 2])
  })
})

describe('OnlineTaskSocket', () => {
  test('URL 按协议和 host 拼接，token 经过 encodeURIComponent', () => {
    const h = harness()
    h.ctl.open()
    expect(h.sockets).toHaveLength(1)
    expect(h.ws().url).toBe(`ws://${location.host}/api/admin/online-modules/ws?token=tok%20en`)
    h.ctl.close()
  })

  test('快照、module、deleted 依次转交给回调；快照到达后才算已连接', () => {
    const h = harness()
    h.ctl.open()
    expect(h.events).toStrictEqual([])
    h.ws().recv({ type: 'snapshot', data: { modules: [mod(1), mod(2)] } })
    h.ws().recv({ type: 'module', data: mod(2, { status: 'failed' }) })
    h.ws().recv({ type: 'deleted', data: { id: 1 } })
    expect(h.events).toStrictEqual(['snapshot:1,2', 'status:true', 'module:2', 'deleted:1'])
    h.ctl.close()
  })

  test('畸形帧和未知类型被忽略，连接不受影响', () => {
    const h = harness()
    h.ctl.open()
    h.ws().recv({ type: 'snapshot', data: { modules: [] } })
    h.events.length = 0
    h.ws().recv('{not json')
    h.ws().recv({ type: 'mystery', data: {} })
    h.ws().recv({ type: 'module', data: { title: '没有 id' } })
    expect(h.events).toStrictEqual([])
    h.ws().recv({ type: 'module', data: mod(5) })
    expect(h.events).toStrictEqual(['module:5'])
    expect(h.ws().closed).toBe(false)
    h.ctl.close()
  })

  test('收到快照后断线：状态回到未连接；之后连续失败按 1、2、4、8、15、15 秒退避', async () => {
    const h = harness()
    h.ctl.open()
    h.ws().recv({ type: 'snapshot', data: { modules: [] } })
    h.ws().drop()
    expect(h.events.at(-1)).toBe('status:false')

    for (const ms of [1000, 2000, 4000, 8000, 15000, 15000]) {
      const before = h.sockets.length
      vi.advanceTimersByTime(ms - 1)
      expect(h.sockets.length, `${ms}ms 的重连不应提前`).toBe(before)
      vi.advanceTimersByTime(1)
      expect(h.sockets.length).toBe(before + 1)
      // 新连接没等到快照就失败：先探测，探测结束才排下一次重连
      h.ws().drop()
      await flush()
    }
    h.ctl.close()
  })

  test('快照到达会重置退避：下一次断线又从 1 秒开始', async () => {
    const h = harness()
    h.ctl.open()
    h.ws().recv({ type: 'snapshot', data: { modules: [] } })
    h.ws().drop()
    vi.advanceTimersByTime(1000)
    h.ws().drop()
    await flush()
    vi.advanceTimersByTime(2000)
    expect(h.sockets).toHaveLength(3)

    h.ws().recv({ type: 'snapshot', data: { modules: [] } })
    h.ws().drop()
    vi.advanceTimersByTime(999)
    expect(h.sockets).toHaveLength(3)
    vi.advanceTimersByTime(1)
    expect(h.sockets).toHaveLength(4)
    h.ctl.close()
  })

  test('握手失败（没收到快照就断开）：先探测 /api/auth/me，再继续退避重连', async () => {
    const h = harness()
    h.ctl.open()
    h.ws().drop()
    // 探测请求立即发出，但重连要等探测结束才排定时器
    expect(h.probes).toStrictEqual(['GET /api/auth/me'])
    expect(vi.getTimerCount()).toBe(0)
    await flush()
    expect(h.probes).toStrictEqual(['GET /api/auth/me'])
    expect(vi.getTimerCount()).toBe(1)
    vi.advanceTimersByTime(999)
    expect(h.sockets).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(h.sockets).toHaveLength(2)

    h.ws().drop()
    await flush()
    vi.advanceTimersByTime(1999)
    expect(h.sockets).toHaveLength(2)
    vi.advanceTimersByTime(1)
    expect(h.sockets).toHaveLength(3)
    h.ctl.close()
  })

  test('探测返回 401（全局处理函数负责登出）：错误被吞掉，仍然排下一次重连，不抛未处理异常', async () => {
    const h = harness({ api: () => Promise.reject(new ApiError('未登录', 401)) })
    h.ctl.open()
    h.ws().drop()
    await flush()
    expect(h.probes).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(1)
    h.ctl.close()
  })

  test('收到过快照的断线不探测', async () => {
    const h = harness()
    h.ctl.open()
    h.ws().recv({ type: 'snapshot', data: { modules: [] } })
    h.ws().drop()
    await flush()
    expect(h.probes).toStrictEqual([])
    h.ctl.close()
  })

  test('网络恢复（online 事件）：不等退避立即重连；连接建立中或已打开时不重复建立', () => {
    const h = harness()
    h.ctl.open()
    h.ws().recv({ type: 'snapshot', data: { modules: [] } })
    h.ws().drop()
    expect(h.sockets).toHaveLength(1)
    window.dispatchEvent(new Event('online'))
    expect(h.sockets).toHaveLength(2)
    // 新连接还在建立
    window.dispatchEvent(new Event('online'))
    expect(h.sockets).toHaveLength(2)
    // 已打开
    h.ws().recv({ type: 'snapshot', data: { modules: [] } })
    window.dispatchEvent(new Event('online'))
    expect(h.sockets).toHaveLength(2)
    h.ctl.close()
  })

  test('close()：断开连接、取消重连定时器、解除 online 监听、状态回到未连接，之后不再重连', async () => {
    const h = harness()
    h.ctl.open()
    h.ws().recv({ type: 'snapshot', data: { modules: [] } })
    const first = h.ws()
    h.ctl.close()
    expect(first.closed).toBe(true)
    expect(first.closeCode).toBe(1000)
    expect(h.events.at(-1)).toBe('status:false')

    // 旧连接迟到的 close 回调和帧都被丢弃
    first.drop()
    first.recv({ type: 'module', data: mod(9) })
    await flush()
    expect(h.events).not.toContain('module:9')
    expect(h.probes).toStrictEqual([])
    window.dispatchEvent(new Event('online'))
    vi.advanceTimersByTime(60_000)
    expect(h.sockets).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  test('重连等待期间 close()：定时器被取消', () => {
    const h = harness()
    h.ctl.open()
    h.ws().recv({ type: 'snapshot', data: { modules: [] } })
    h.ws().drop()
    expect(vi.getTimerCount()).toBe(1)
    h.ctl.close()
    expect(vi.getTimerCount()).toBe(0)
    vi.advanceTimersByTime(60_000)
    expect(h.sockets).toHaveLength(1)
  })

  test('探测进行中 close()：探测结束后不会再排重连', async () => {
    let finish: () => void = () => {}
    const h = harness({ api: () => new Promise((res) => (finish = () => res({}))) })
    h.ctl.open()
    h.ws().drop()
    h.ctl.close()
    finish()
    await flush()
    expect(vi.getTimerCount()).toBe(0)
  })

  test('重复 open() 只建立一个连接；close() 后可以重新 open()', () => {
    const h = harness()
    h.ctl.open()
    h.ctl.open()
    expect(h.sockets).toHaveLength(1)
    h.ctl.close()
    h.ctl.open()
    expect(h.sockets).toHaveLength(2)
    h.ctl.close()
  })

  test('服务端以 1013 关闭慢客户端：当作普通断线，重连后重新拿快照', () => {
    const h = harness()
    h.ctl.open()
    h.ws().recv({ type: 'snapshot', data: { modules: [mod(1)] } })
    h.ws().drop()
    vi.advanceTimersByTime(1000)
    h.ws().recv({ type: 'snapshot', data: { modules: [mod(1), mod(2)] } })
    expect(h.events.filter((e) => e.startsWith('snapshot'))).toStrictEqual(['snapshot:1', 'snapshot:1,2'])
    h.ctl.close()
  })

  test('createSocket 抛错（如 URL 非法）：按失败处理并退避重连', () => {
    const calls: string[] = []
    const ctl = new OnlineTaskSocket({
      onSnapshot: vi.fn(),
      onModule: vi.fn(),
      onDeleted: vi.fn(),
      onStatus: vi.fn(),
      token: () => 't',
      createSocket: (url) => {
        calls.push(url)
        throw new Error('bad url')
      },
    })
    ctl.open()
    expect(calls).toHaveLength(1)
    vi.advanceTimersByTime(1000)
    expect(calls).toHaveLength(2)
    ctl.close()
  })
})
