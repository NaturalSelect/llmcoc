// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import type { InventoryItem } from '@/api/characterTypes'
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
    await characterApi.removeInventoryItem(3, { name: '绷带 (x3)/大', desc: '染血/旧' })
    expect(lastCall()).toEqual({
      path: '/api/characters/3/inventory/%E7%BB%B7%E5%B8%A6%20(x3)%2F%E5%A4%A7?desc=%E6%9F%93%E8%A1%80%2F%E6%97%A7',
      method: 'DELETE',
      body: undefined,
    })
    await characterApi.removeSocialRelation(3, '约翰 & 玛丽')
    expect(lastCall().path).toBe('/api/characters/3/social-relations/%E7%BA%A6%E7%BF%B0%20%26%20%E7%8E%9B%E4%B8%BD')
    await characterApi.removeAsset(3, '老宅')
    expect(lastCall().path).toBe('/api/characters/3/assets/%E8%80%81%E5%AE%85')
    await characterApi.forgetSpell(3, '束缚术')
    expect(lastCall()).toMatchObject({ path: '/api/characters/3/spells/%E6%9D%9F%E7%BC%9A%E6%9C%AF', method: 'DELETE' })
    await characterApi.discard({ name: '绷带/旧', desc: 'a b' })
    expect(lastCall()).toMatchObject({ path: '/api/warehouse/items/%E7%BB%B7%E5%B8%A6%2F%E6%97%A7?desc=a%20b', method: 'DELETE' })
  })

  test('物品删除：无描述时 desc 为空串；count 缺省或 0 不带参数，正数带 count', async () => {
    await characterApi.removeInventoryItem(1, { name: '绷带' })
    expect(lastCall().path).toBe('/api/characters/1/inventory/%E7%BB%B7%E5%B8%A6?desc=')
    await characterApi.removeInventoryItem(1, { name: '绷带' }, 0)
    expect(lastCall().path).toBe('/api/characters/1/inventory/%E7%BB%B7%E5%B8%A6?desc=')
    await characterApi.removeInventoryItem(1, { name: '绷带', desc: '染血' }, 2)
    expect(lastCall().path).toBe('/api/characters/1/inventory/%E7%BB%B7%E5%B8%A6?desc=%E6%9F%93%E8%A1%80&count=2')
    await characterApi.discard({ name: '地图' }, 1)
    expect(lastCall().path).toBe('/api/warehouse/items/%E5%9C%B0%E5%9B%BE?desc=&count=1')
  })

  test('重新生成：三个端点，请求体只带 guidance', async () => {
    for (const field of ['appearance', 'backstory', 'traits'] as const) {
      await characterApi.regenerate(9, field, '戴眼镜')
      expect(lastCall()).toEqual({ path: `/api/characters/9/regenerate-${field}`, method: 'POST', body: { guidance: '戴眼镜' } })
    }
  })

  test('仓库存取请求体：character_card_id + item{name,desc} + count（缺省不发送，即整堆）', async () => {
    const stack: InventoryItem = { name: '绷带', desc: '染血', count: 3, type: 'misc' }
    await characterApi.deposit(4, stack)
    expect(lastCall()).toEqual({ path: '/api/warehouse/deposit', method: 'POST', body: { character_card_id: 4, item: { name: '绷带', desc: '染血' } } })
    await characterApi.withdraw(4, { name: '绷带' }, 2)
    expect(lastCall()).toEqual({ path: '/api/warehouse/withdraw', method: 'POST', body: { character_card_id: 4, item: { name: '绷带', desc: '' }, count: 2 } })
  })

  test('装备与卸下：PUT inventory/equip，请求体 item{name,desc} + equipped', async () => {
    const stack: InventoryItem = { name: '手电筒', desc: '黄铜', count: 1, type: 'equipment' }
    await characterApi.setEquipped(4, stack, true)
    expect(lastCall()).toEqual({
      path: '/api/characters/4/inventory/equip',
      method: 'PUT',
      body: { item: { name: '手电筒', desc: '黄铜' }, equipped: true },
    })
    await characterApi.setEquipped(4, { name: '绷带' }, false)
    expect(lastCall()).toEqual({
      path: '/api/characters/4/inventory/equip',
      method: 'PUT',
      body: { item: { name: '绷带', desc: '' }, equipped: false },
    })
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
