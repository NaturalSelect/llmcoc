import type { CharacterCard } from '@/api/types'
import type { ShopItem } from '@/api/shopTypes'

export function isInventoryItem(itemType: string): boolean {
  return itemType === 'equipment' || itemType === 'weapon' || itemType === 'accessory'
}

export function isReviveItem(itemType: string): boolean {
  return itemType === 'revive'
}

export function requiresCharacter(itemType: string): boolean {
  return isInventoryItem(itemType) || isReviveItem(itemType)
}

const TYPE_LABEL: Record<string, string> = {
  card_slot: '卡槽',
  coins: '金币',
  weapon: '武器',
  equipment: '装备',
  revive: '复活',
}

export function itemTypeLabel(itemType: string): string {
  return TYPE_LABEL[itemType] || '配件'
}

export type PurchasePlan =
  | { ok: true; confirmMessage: string; confirmText: string; characterId: number | null }
  | { ok: false; error: string }

// NOTE: 旧前端对装备类商品先弹确认、确认后才提示没选人物卡；这里改为先校验再确认，避免用户确认完才被拒绝
export function planPurchase(item: ShopItem, targetId: number | '' | null, characters: CharacterCard[]): PurchasePlan {
  const chosen = targetId === '' || targetId == null ? 0 : Number(targetId)
  if (isReviveItem(item.item_type)) {
    if (!chosen) return { ok: false, error: '请先选择要复活的人物卡' }
    const target = characters.find((c) => c.id === chosen)
    if (!target || target.wound_state !== 'dead') return { ok: false, error: '所选人物卡并非死亡状态，无法使用复活卷轴' }
    return {
      ok: true,
      confirmMessage: `确认花费 ${item.price} 金币复活「${target.name}」？\n复活后HP为最大值的一半，随机失去一半装备，遗忘一半法术。`,
      confirmText: '复活',
      characterId: chosen,
    }
  }
  if (isInventoryItem(item.item_type) && !chosen) {
    return { ok: false, error: '请先选择目标人物卡' }
  }
  return {
    ok: true,
    confirmMessage: `确认花费 ${item.price} 金币购买「${item.name}」？`,
    confirmText: '购买',
    characterId: requiresCharacter(item.item_type) ? chosen : null,
  }
}
