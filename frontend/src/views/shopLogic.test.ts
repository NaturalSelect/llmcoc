// @vitest-environment node
import { describe, expect, test } from 'vitest'
import type { ShopItem } from '@/api/shopTypes'
import { isInventoryItem, itemTypeLabel, planPurchase, requiresCharacter } from './shopLogic'

const item = (over: Partial<ShopItem>): ShopItem => ({ id: 1, name: '手枪', item_type: 'weapon', price: 50, ...over })
const chars = [
  { id: 1, name: '艾米', wound_state: 'dead' },
  { id: 2, name: '鲍勃', wound_state: 'healthy' },
]

describe('shopLogic', () => {
  test('商品类型判断', () => {
    expect(isInventoryItem('weapon')).toBe(true)
    expect(isInventoryItem('accessory')).toBe(true)
    expect(isInventoryItem('revive')).toBe(false)
    expect(requiresCharacter('revive')).toBe(true)
    expect(requiresCharacter('coins')).toBe(false)
    expect(requiresCharacter('card_slot')).toBe(false)
  })

  test('类型标签：未知类型归为配件', () => {
    expect(itemTypeLabel('card_slot')).toBe('卡槽')
    expect(itemTypeLabel('accessory')).toBe('配件')
    expect(itemTypeLabel('whatever')).toBe('配件')
  })

  test('金币与卡槽商品不需要人物卡', () => {
    const plan = planPurchase(item({ item_type: 'coins', name: '金币包' }), '', chars)
    expect(plan).toEqual({ ok: true, confirmMessage: '确认花费 50 金币购买「金币包」？', confirmText: '购买', characterId: null })
  })

  test('装备类商品必须先选人物卡', () => {
    expect(planPurchase(item({}), '', chars)).toEqual({ ok: false, error: '请先选择目标人物卡' })
    const plan = planPurchase(item({}), 2, chars)
    expect(plan.ok && plan.characterId).toBe(2)
  })

  test('复活卷轴：未选、选了活人、选了死者三种情况', () => {
    const revive = item({ item_type: 'revive', name: '复活卷轴', price: 300 })
    expect(planPurchase(revive, '', chars)).toEqual({ ok: false, error: '请先选择要复活的人物卡' })
    expect(planPurchase(revive, 2, chars)).toEqual({ ok: false, error: '所选人物卡并非死亡状态，无法使用复活卷轴' })
    expect(planPurchase(revive, 99, chars)).toEqual({ ok: false, error: '所选人物卡并非死亡状态，无法使用复活卷轴' })
    const ok = planPurchase(revive, 1, chars)
    expect(ok.ok).toBe(true)
    if (ok.ok) {
      expect(ok.confirmText).toBe('复活')
      expect(ok.characterId).toBe(1)
      expect(ok.confirmMessage).toContain('复活「艾米」')
      expect(ok.confirmMessage).toContain('遗忘一半法术')
    }
  })
})
