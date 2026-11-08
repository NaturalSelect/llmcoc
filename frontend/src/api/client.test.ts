// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { ApiError, api, postForm, setUnauthorizedHandler } from './client'
import { fetchAllPages } from './pagination'

function respond(status: number, body: unknown, ok = status >= 200 && status < 300) {
  return { ok, status, json: async () => body } as Response
}

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  setUnauthorizedHandler(null)
})

describe('api', () => {
  test('带 token 和 JSON 头；GET 不发送请求体', async () => {
    localStorage.setItem('coc_token', 'abc')
    const fn = vi.fn(async () => respond(200, { ok: 1 }))
    vi.stubGlobal('fetch', fn)

    expect(await api('GET', '/api/x', { ignored: true })).toStrictEqual({ ok: 1 })
    await api('POST', '/api/y', { a: 1 })
    const [, get] = fn.mock.calls[0] as unknown as [string, RequestInit]
    const [, post] = fn.mock.calls[1] as unknown as [string, RequestInit]
    expect(get.body).toBeUndefined()
    expect(post.body).toBe('{"a":1}')
    expect((post.headers as Record<string, string>).Authorization).toBe('Bearer abc')
    expect((post.headers as Record<string, string>)['Content-Type']).toBe('application/json')
  })

  test('没有 token 时不带 Authorization', async () => {
    const fn = vi.fn(async () => respond(200, {}))
    vi.stubGlobal('fetch', fn)
    await api('GET', '/api/x')
    const [, init] = fn.mock.calls[0] as unknown as [string, RequestInit]
    expect((init.headers as Record<string, string>).Authorization).toBeUndefined()
  })

  test('失败时抛 ApiError，带状态码，details 拼进消息', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => respond(400, { error: '参数错误', details: ['a 必填', 'b 太长'] })))
    const err = await api('POST', '/api/x', {}).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(400)
    expect(err.message).toBe('参数错误：a 必填；b 太长')
    expect(err.details).toStrictEqual(['a 必填', 'b 太长'])
  })

  test('响应不是 JSON 时回落到 HTTP 状态码', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: false, status: 502, json: async () => Promise.reject(new Error('x')) }) as unknown as Response),
    )
    const err = await api('GET', '/api/x').catch((e) => e)
    expect(err.message).toBe('HTTP 502')
  })

  test('带 token 的请求返回 401 触发统一回调；登录接口的 401 和无 token 的 401 不触发', async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    vi.stubGlobal('fetch', vi.fn(async () => respond(401, { error: '未登录' })))

    await api('GET', '/api/x').catch(() => {})
    expect(handler).not.toHaveBeenCalled()

    localStorage.setItem('coc_token', 'abc')
    await api('POST', '/api/auth/login', {}).catch(() => {})
    await api('POST', '/api/auth/register', {}).catch(() => {})
    expect(handler).not.toHaveBeenCalled()

    const err = await api('GET', '/api/sessions').catch((e) => e)
    expect(handler).toHaveBeenCalledTimes(1)
    expect(err.status).toBe(401)
  })
})

describe('postForm', () => {
  test('不因状态码抛错，返回状态码和响应体；网络失败才抛出', async () => {
    localStorage.setItem('coc_token', 'abc')
    const fn = vi.fn(async () => respond(409, { error: 'KP 正在推进剧情' }))
    vi.stubGlobal('fetch', fn)
    const form = new FormData()
    form.append('content', 'hi')
    expect(await postForm('/api/sessions/1/chat', form)).toStrictEqual({ status: 409, data: { error: 'KP 正在推进剧情' } })
    const [, init] = fn.mock.calls[0] as unknown as [string, RequestInit]
    expect(init.body).toBe(form)
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer abc')
    expect((init.headers as Record<string, string>)['Content-Type']).toBeUndefined()

    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('network'))))
    await expect(postForm('/x', form)).rejects.toThrow('network')
  })
})

describe('fetchAllPages', () => {
  test('逐页拉取直到最后一页，参数拼接正确，onProgress 每页回调一次', async () => {
    vi.useFakeTimers()
    const urls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        urls.push(url)
        const page = Number(new URL(url, 'http://x').searchParams.get('page'))
        return respond(200, { items: [`p${page}`], total_pages: 3 })
      }),
    )
    const progress: string[][] = []
    const p = fetchAllPages<string>('/api/characters', { onProgress: (all) => progress.push(all) })
    await vi.runAllTimersAsync()
    expect(await p).toStrictEqual(['p1', 'p2', 'p3'])
    expect(urls).toStrictEqual([
      '/api/characters?page=1&page_size=100',
      '/api/characters?page=2&page_size=100',
      '/api/characters?page=3&page_size=100',
    ])
    expect(progress).toStrictEqual([['p1'], ['p1', 'p2'], ['p1', 'p2', 'p3']])
  })

  test('路径已有查询串时用 & 拼接；缺省的 total_pages 视为一页', async () => {
    const urls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        urls.push(url)
        return respond(200, { items: null })
      }),
    )
    expect(await fetchAllPages('/api/x?status=a', { pageSize: 5 })).toStrictEqual([])
    expect(urls).toStrictEqual(['/api/x?status=a&page=1&page_size=5'])
  })
})
