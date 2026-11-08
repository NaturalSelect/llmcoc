import type { CharacterCard } from './types'

export interface ShopItem {
  id: number
  name: string
  description?: string
  item_type: string
  price: number
  value?: number
  is_active?: boolean
}

export interface ShopTransaction {
  id: number
  coins_spent: number
  created_at?: string
  shop_item?: { name?: string } | null
}

export interface PurchaseResult {
  coins: number
  card_slots: number
  character_card?: CharacterCard | null
}
