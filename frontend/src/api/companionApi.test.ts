// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { ApiError } from './client'
import { companionApi } from './companionApi'

function respond(body: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => body } as Response
}

let fn: ReturnType<typeof vi.fn>

beforeEach(() => {
  localStorage.clear()
  fn = vi.fn(async () => respond({}))
  vi.stubGlobal('fetch', fn)
})

afterEach(() => vi.unstubAllGlobals())

function lastCall(): { path: string; method: string; body: unknown } {
  const [path, init] = fn.mock.calls.at(-1) as unknown as [string, RequestInit]
  return { path, method: init.method as string, body: init.body ? JSON.parse(init.body as string) : undefined }
}

describe('companionApi 房间内接口', () => {
  test('cards：GET 选卡接口，enabled 与卡片原样返回', async () => {
    const card = { id: 5, name: '雷蒙', occupation: '侦探', age: 31, gender: '男', portrait_hash: 'AB12', hp: 10, max_hp: 11, san: 55, max_san: 60 }
    fn.mockResolvedValueOnce(respond({ enabled: true, cards: [card] }))
    expect(await companionApi.cards(7)).toStrictEqual({ enabled: true, cards: [card] })
    expect(lastCall()).toEqual({ path: '/api/sessions/7/companion-cards', method: 'GET', body: undefined })
  })

  test('cards：cards 为 null、enabled 缺省时按未启用的空列表处理', async () => {
    fn.mockResolvedValueOnce(respond({ enabled: false, cards: null }))
    expect(await companionApi.cards(7)).toStrictEqual({ enabled: false, cards: [] })
    fn.mockResolvedValueOnce(respond({}))
    expect(await companionApi.cards(7)).toStrictEqual({ enabled: false, cards: [] })
    fn.mockResolvedValueOnce(respond(null))
    expect(await companionApi.cards(7)).toStrictEqual({ enabled: false, cards: [] })
  })

  test('add：POST 请求体原样提交，响应带回 handout_code', async () => {
    fn.mockResolvedValueOnce(respond({ handout_code: 'HO2' }))
    const res = await companionApi.add(7, { character_card_id: 5, handout_code: 'random' })
    expect(res.handout_code).toBe('HO2')
    expect(lastCall()).toEqual({
      path: '/api/sessions/7/companion',
      method: 'POST',
      body: { character_card_id: 5, handout_code: 'random' },
    })
  })

  test('add：409（房间里已有 AI）抛出带状态码和服务端文案的 ApiError', async () => {
    fn.mockResolvedValueOnce(respond({ error: '房间里已经有 AI 陪玩' }, 409))
    const err = await companionApi.add(7, { character_card_id: 5 }).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 409, message: '房间里已经有 AI 陪玩' })
  })

  test('remove：DELETE 同一路径，不带请求体', async () => {
    await companionApi.remove(7)
    expect(lastCall()).toEqual({ path: '/api/sessions/7/companion', method: 'DELETE', body: undefined })
  })
})

describe('companionApi 管理员卡池接口', () => {
  test('adminList：返回数组，null 按空数组', async () => {
    const row = { id: 5, name: '雷蒙', is_active: true, wound_state: 'none', in_session: false }
    fn.mockResolvedValueOnce(respond([row]))
    expect(await companionApi.adminList()).toStrictEqual([row])
    expect(lastCall()).toEqual({ path: '/api/admin/companion-cards', method: 'GET', body: undefined })
    fn.mockResolvedValueOnce(respond(null))
    expect(await companionApi.adminList()).toStrictEqual([])
  })

  test('adminAdd 请求体是 card_id；adminReturn 走 :id/return', async () => {
    await companionApi.adminAdd(5)
    expect(lastCall()).toEqual({ path: '/api/admin/companion-cards', method: 'POST', body: { card_id: 5 } })
    await companionApi.adminReturn(5)
    expect(lastCall()).toEqual({ path: '/api/admin/companion-cards/5/return', method: 'POST', body: undefined })
  })
})
