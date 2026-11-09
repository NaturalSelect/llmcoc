// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { characterApi } from './characterApi'

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

describe('characterApi 路径与请求体', () => {
  test('路径里的名称必须 encodeURIComponent（中文、斜杠、空格）', async () => {
    await characterApi.removeInventoryItem(3, '绷带 (x3)/大')
    expect(lastCall()).toEqual({ path: '/api/characters/3/inventory/%E7%BB%B7%E5%B8%A6%20(x3)%2F%E5%A4%A7', method: 'DELETE', body: undefined })
    await characterApi.removeSocialRelation(3, '约翰 & 玛丽')
    expect(lastCall().path).toBe('/api/characters/3/social-relations/%E7%BA%A6%E7%BF%B0%20%26%20%E7%8E%9B%E4%B8%BD')
    await characterApi.removeAsset(3, '老宅')
    expect(lastCall().path).toBe('/api/characters/3/assets/%E8%80%81%E5%AE%85')
    await characterApi.forgetSpell(3, '束缚术')
    expect(lastCall()).toMatchObject({ path: '/api/characters/3/spells/%E6%9D%9F%E7%BC%9A%E6%9C%AF', method: 'DELETE' })
    await characterApi.discard('绷带/旧')
    expect(lastCall()).toMatchObject({ path: '/api/warehouse/items/%E7%BB%B7%E5%B8%A6%2F%E6%97%A7', method: 'DELETE' })
  })

  test('重新生成：三个端点，请求体只带 guidance', async () => {
    for (const field of ['appearance', 'backstory', 'traits'] as const) {
      await characterApi.regenerate(9, field, '戴眼镜')
      expect(lastCall()).toEqual({ path: `/api/characters/9/regenerate-${field}`, method: 'POST', body: { guidance: '戴眼镜' } })
    }
  })

  test('仓库存取请求体字段为 character_card_id + item', async () => {
    await characterApi.deposit(4, '绷带')
    expect(lastCall()).toEqual({ path: '/api/warehouse/deposit', method: 'POST', body: { character_card_id: 4, item: '绷带' } })
    await characterApi.withdraw(4, '绷带')
    expect(lastCall()).toEqual({ path: '/api/warehouse/withdraw', method: 'POST', body: { character_card_id: 4, item: '绷带' } })
  })

  test('删除、彻底删除、复活、掷骰、技能默认值', async () => {
    await characterApi.get(5)
    expect(lastCall()).toMatchObject({ path: '/api/characters/5', method: 'GET' })
    await characterApi.remove(5)
    expect(lastCall()).toMatchObject({ path: '/api/characters/5', method: 'DELETE' })
    await characterApi.removeDead(5)
    expect(lastCall()).toMatchObject({ path: '/api/characters/5/dead', method: 'DELETE' })
    await characterApi.revive(5)
    expect(lastCall()).toMatchObject({ path: '/api/characters/5/revive', method: 'POST' })
    await characterApi.roll(30)
    expect(lastCall()).toEqual({ path: '/api/characters/roll', method: 'POST', body: { age: 30 } })
    await characterApi.skillDefaults()
    expect(lastCall()).toMatchObject({ path: '/api/characters/skill-defaults', method: 'GET' })
  })

  test('最近房间只拉第一页', async () => {
    await characterApi.recentSessions(6)
    expect(lastCall()).toMatchObject({ path: '/api/sessions?page=1&page_size=6', method: 'GET' })
  })

  test('后端错误以 ApiError 抛出，消息可直接展示', async () => {
    fn.mockResolvedValueOnce(respond({ error: '金币不足' }, 402))
    await expect(characterApi.regenerate(1, 'traits', '')).rejects.toThrow('金币不足')
  })
})
