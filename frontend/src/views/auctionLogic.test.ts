// @vitest-environment node
import { describe, expect, test } from 'vitest'
import type { AuctionListing } from '@/api/auctionTypes'
import type { InventoryItem } from '@/api/characterTypes'
import { browseQuery, buyBlocker, listingLabel, planListing, statusLabel, statusTone } from './auctionLogic'

const listing = (over: Partial<AuctionListing> = {}): AuctionListing => ({
  id: 1,
  seller_id: 2,
  item_name: '绷带',
  item_desc: '',
  item_type: 'misc',
  count: 1,
  price: 50,
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
  ...over,
})
const bandage: InventoryItem = { name: '绷带', desc: '染血', count: 3, type: 'misc' }

describe('auctionLogic', () => {
  test('浏览查询串：空搜索词和空类型不带参数，搜索词按 URL 编码', () => {
    expect(browseQuery({ q: '  ', type: '', sort: 'newest', page: 1, pageSize: 12 })).toBe('page=1&page_size=12&sort=newest')
    expect(browseQuery({ q: ' 绷带 ', type: 'weapon', sort: 'price_asc', page: 3, pageSize: 12 })).toBe(
      'page=3&page_size=12&sort=price_asc&q=%E7%BB%B7%E5%B8%A6&type=weapon',
    )
  })

  test('挂单标签与后端 Item.Label 一致：名称(描述, xN)', () => {
    expect(listingLabel(listing())).toBe('绷带')
    expect(listingLabel(listing({ count: 2 }))).toBe('绷带(x2)')
    expect(listingLabel(listing({ item_desc: '染血', count: 2 }))).toBe('绷带(染血, x2)')
  })

  test('状态文案与色调', () => {
    expect(statusLabel('active')).toBe('在售')
    expect(statusLabel('sold')).toBe('已售出')
    expect(statusLabel('cancelled')).toBe('已下架')
    expect(statusTone('active')).toBe('seal')
    expect(statusTone('sold')).toBe('moss')
    expect(statusTone('cancelled')).toBe('faint')
  })

  test('购买拦截：自己的挂单优先于金币不足，金币刚好够可以买', () => {
    expect(buyBlocker(listing({ seller_id: 9 }), 9, 0)).toBe('这是你自己的挂单')
    expect(buyBlocker(listing({ price: 50 }), 9, 49)).toBe('金币不足')
    expect(buyBlocker(listing({ price: 50 }), 9, 50)).toBeNull()
    expect(buyBlocker(listing(), undefined, 100)).toBeNull()
  })

  test('上架校验：必须选物品，数量 1 到库存，价格正整数', () => {
    expect(planListing(null, 1, 10)).toEqual({ ok: false, error: '请选择要上架的物品' })
    for (const bad of ['', 0, -1, 1.5, 'abc']) {
      expect(planListing(bandage, bad, 10)).toEqual({ ok: false, error: '数量必须是正整数' })
    }
    expect(planListing(bandage, 4, 10)).toEqual({ ok: false, error: '仓库里只有 3 件' })
    for (const bad of ['', 0, -5, 0.5, 'abc', Number.MAX_SAFE_INTEGER + 2]) {
      expect(planListing(bandage, 1, bad)).toEqual({ ok: false, error: '价格必须是正整数' })
    }
  })

  test('上架确认文案按所选数量而不是库存数量', () => {
    expect(planListing(bandage, '2', '80')).toEqual({
      ok: true,
      count: 2,
      price: 80,
      confirmMessage: '确认以 80 金币上架「绷带(染血, x2)」？\n上架后物品会从仓库移出，下架后退回仓库。',
    })
    expect(planListing(bandage, 3, 1)).toMatchObject({ ok: true, count: 3, price: 1 })
  })
})
