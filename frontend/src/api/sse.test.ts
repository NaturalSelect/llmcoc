// @vitest-environment jsdom
import { afterEach, describe, expect, test, vi } from 'vitest'
import { ApiError } from './client'
import { streamSSE } from './sse'

type Bytes = ReadableStream<Uint8Array<ArrayBuffer>>

function streamOf(chunks: string[]): Bytes {
  const enc = new TextEncoder()
  return new ReadableStream({
    start(controller) {
      for (const c of chunks) controller.enqueue(enc.encode(c))
      controller.close()
    },
  })
}

function mockFetch(resp: Partial<Response> & { body?: Bytes | null }) {
  const fn = vi.fn(async () => resp as Response)
  vi.stubGlobal('fetch', fn)
  return fn
}

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

async function collect(chunks: string[]) {
  mockFetch({ ok: true, status: 200, body: streamOf(chunks) })
  const events: Array<[string, string]> = []
  await streamSSE('/api/x', {}, (e, d) => events.push([e, d]))
  return events
}

describe('streamSSE', () => {
  test('按 event/data 行解析，空行重置事件名', async () => {
    const events = await collect([
      'event: progress\ndata: {"a":1}\n\n',
      'event: done\ndata: {"name":"x"}\n\n',
      'data: orphan\n',
    ])
    expect(events).toStrictEqual([
      ['progress', '{"a":1}'],
      ['done', '{"name":"x"}'],
      ['', 'orphan'],
    ])
  })

  test('一行被拆在两个分块里、CRLF 换行、data 冒号后无空格都能解析', async () => {
    const events = await collect(['event: prog', 'ress\r\ndata:no-space\r\n', '\r\nevent:error\r\ndata: 失败\r\n\r\n'])
    expect(events).toStrictEqual([
      ['progress', 'no-space'],
      ['error', '失败'],
    ])
  })

  test('多字节字符被拆在分块中间也不乱码', async () => {
    const bytes = new TextEncoder().encode('event: progress\ndata: 雨夜\n\n')
    const cut = bytes.indexOf(0xe9) + 1
    const body: Bytes = new ReadableStream({
      start(c) {
        c.enqueue(bytes.slice(0, cut))
        c.enqueue(bytes.slice(cut))
        c.close()
      },
    })
    mockFetch({ ok: true, status: 200, body })
    const events: Array<[string, string]> = []
    await streamSSE('/api/x', {}, (e, d) => events.push([e, d]))
    expect(events).toStrictEqual([['progress', '雨夜']])
  })

  test('同一事件的多行 data 各触发一次，不拼接', async () => {
    const events = await collect(['event: progress\ndata: 一\ndata: 二\n\n'])
    expect(events).toStrictEqual([
      ['progress', '一'],
      ['progress', '二'],
    ])
  })

  test('请求带 token 和 JSON 请求体', async () => {
    localStorage.setItem('coc_token', 'abc')
    const fn = mockFetch({ ok: true, status: 200, body: streamOf([]) })
    await streamSSE('/api/scenarios/generate', { count: 2 }, () => {})
    const [path, init] = fn.mock.calls[0] as unknown as [string, RequestInit]
    expect(path).toBe('/api/scenarios/generate')
    expect(init.method).toBe('POST')
    expect(init.body).toBe('{"count":2}')
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer abc')
  })

  test('握手失败抛 ApiError，带后端错误信息和状态码', async () => {
    mockFetch({ ok: false, status: 403, json: async () => ({ error: '需要管理员权限' }) })
    const err = await streamSSE('/api/x', {}, () => {}).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.message).toBe('需要管理员权限')
    expect(err.status).toBe(403)

    mockFetch({ ok: false, status: 502, json: async () => Promise.reject(new Error('not json')) })
    const err2 = await streamSSE('/api/x', {}, () => {}).catch((e) => e)
    expect(err2.message).toBe('请求失败')
  })
})
