import type { ShopItemForm } from '@/api/adminTypesContent'

export const SHOP_ITEM_TYPES = [
  { value: 'card_slot', label: '人物卡槽位' },
  { value: 'coins', label: '金币' },
  { value: 'equipment', label: '基础装备' },
  { value: 'weapon', label: '武器' },
  { value: 'accessory', label: '配件' },
] as const

export function emptyShopItemForm(): ShopItemForm {
  return { name: '', description: '', item_type: 'card_slot', price: 0, value: 1, is_active: true }
}
