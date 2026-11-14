// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { ApiError } from './client'
import { runJobSocket } from './jobSocket'

class FakeWS {
  static all: FakeWS[] = []
  onopen: (() => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  sent: string[] = []
  closed = false
  constructor(readonly url: string) {
    FakeWS.all.push(this)
  }
  send(data: string) {
    this.sent.push(data)
  }
  close() {
    this.closed = true
  }
  open() {
    this.onopen?.()
  }
  recv(type: string, data: unknown) {
    this.onmessage?.({ data: JSON.stringify({ type, data }) } as MessageEvent)
  }
  recvRaw(data: unknown) {
    this.onmessage?.({ data } as MessageEvent)
  }
  end() {
    this.onclose?.()
  }
}

beforeEach(() => {
  FakeWS.all = []
  localStorage.setItem('coc_token', 'tok en')
  vi.stubGlobal('WebSocket', FakeWS)
})

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

function start(opts?: Parameters<typeof runJobSocket>[3]) {
  const events: Array<[string, string]> = []
  const p = runJobSocket('/api/scenarios/generate/ws', { count: 2 }, (e, d) => events.push([e, d]), opts)
  return { events, p, ws: FakeWS.all[0] }
}

describe('runJobSocket', () => {
  test('url 带转义后的 token，握手完成后才发请求帧', async () => {
    const { p, ws } = start()
    expect(ws.url).toMatch(/^wss?:\/\/[^/]+\/api\/scenarios\/generate\/ws\?token=tok%20en$/)
    expect(ws.sent).toStrictEqual([])
    ws.open()
    expect(ws.sent.map((s) => JSON.parse(s))).toStrictEqual([{ count: 2 }])
    ws.end()
    await expect(p).resolves.toBeUndefined()
  })

  test('帧转成 (事件名, JSON 字符串) 回调，服务端关闭连接时正常结束', async () => {
    const { events, p, ws } = start()
    ws.open()
    ws.recv('progress', { stage: 'queued', detail: '排队中' })
    ws.recv('done', { index: 1, scenario_id: 7, name: '雨夜档案' })
    ws.end()
    await p
    expect(events.map(([e, d]) => [e, JSON.parse(d)])).toStrictEqual([
      ['progress', { stage: 'queued', detail: '排队中' }],
      ['done', { index: 1, scenario_id: 7, name: '雨夜档案' }],
    ])
  })

  test('畸形帧（非字符串、非 JSON、缺 type）被丢弃，不影响后续帧', async () => {
    const { events, p, ws } = start()
    ws.open()
    ws.recvRaw(new ArrayBuffer(4))
    ws.recvRaw('not json')
    ws.recvRaw('null')
    ws.recvRaw(JSON.stringify({ data: 1 }))
    ws.recv('progress', { detail: 'ok' })
    ws.end()
    await p
    expect(events).toHaveLength(1)
    expect(events[0][0]).toBe('progress')
  })

  test('rejected 帧：不当作事件回调，Promise 以 ApiError(400) 失败并带拒绝原因', async () => {
    const { events, p, ws } = start()
    ws.open()
    ws.recv('rejected', { message: '生成数量不能超过 10' })
    ws.end()
    const err = await p.catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.message).toBe('生成数量不能超过 10')
    expect(err.status).toBe(400)
    expect(events).toStrictEqual([])
  })

  test('握手前就断开：探测一次登录态，再以 ApiError 失败', async () => {
    const fetchFn = vi.fn(async () => ({ ok: false, status: 401, json: async () => ({ error: '令牌无效' }) }) as unknown as Response)
    vi.stubGlobal('fetch', fetchFn)
    const { p, ws } = start()
    ws.end()
    const err = await p.catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(fetchFn).toHaveBeenCalledTimes(1)
    expect((fetchFn.mock.calls[0] as unknown as [string])[0]).toBe('/api/auth/me')
  })

  test('握手后连接中途断开：算正常结束，由调用方按是否收到终结事件判断', async () => {
    const { events, p, ws } = start()
    ws.open()
    ws.recv('progress', { detail: '进行中' })
    ws.end()
    await expect(p).resolves.toBeUndefined()
    expect(events).toHaveLength(1)
  })

  test('abort：关闭连接并立即成功，之后迟到的帧不再回调', async () => {
    const ctrl = new AbortController()
    const { events, p, ws } = start({ signal: ctrl.signal })
    ws.open()
    ws.recv('progress', { detail: '一' })
    ctrl.abort()
    await expect(p).resolves.toBeUndefined()
    expect(ws.closed).toBe(true)
    ws.recv('progress', { detail: '二' })
    ws.end()
    expect(events).toHaveLength(1)
  })

  test('调用前已 abort：不建立连接', async () => {
    const ctrl = new AbortController()
    ctrl.abort()
    await expect(runJobSocket('/api/x/ws', {}, () => {}, { signal: ctrl.signal })).resolves.toBeUndefined()
    expect(FakeWS.all).toHaveLength(0)
  })

  test('构造 WebSocket 抛错时 Promise 失败', async () => {
    const boom = new Error('bad url')
    const p = runJobSocket('/api/x/ws', {}, () => {}, {
      createSocket: () => {
        throw boom
      },
    })
    await expect(p).rejects.toBe(boom)
  })
})
