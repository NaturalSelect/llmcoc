import type { InventoryItem, InventoryItemType } from './characterTypes'

export type AuctionStatus = 'active' | 'sold' | 'cancelled'

export type AuctionSort = 'newest' | 'price_asc' | 'price_desc'

// NOTE: item_desc 为空时后端给空串；seller_name 只在列表接口里有，创建/购买/下架的响应不带
export interface AuctionListing {
  id: number
  seller_id: number
  seller_name?: string
  buyer_id?: number | null
  item_name: string
  item_desc?: string
  item_type: InventoryItemType
  count: number
  price: number
  status: AuctionStatus
  created_at: string
  closed_at?: string | null
}

export interface AuctionListingResult {
  message?: string
  listing: AuctionListing
  warehouse: InventoryItem[] | null
}

export interface AuctionBuyResult extends AuctionListingResult {
  coins: number
}
