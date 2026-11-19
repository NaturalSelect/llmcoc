import type { AuctionListing, AuctionSort, AuctionStatus } from '@/api/auctionTypes'
import type { InventoryItem } from '@/api/characterTypes'
import { itemLabel } from '@/components/character/characterLogic'

export const SORT_OPTIONS: Array<{ key: AuctionSort; label: string }> = [
  { key: 'newest', label: '最新上架' },
  { key: 'price_asc', label: '价格从低到高' },
  { key: 'price_desc', label: '价格从高到低' },
]

const STATUS_LABEL: Record<AuctionStatus, string> = { active: '在售', sold: '已售出', cancelled: '已下架' }

export function statusLabel(status: AuctionStatus): string {
  return STATUS_LABEL[status] ?? status
}

export function statusTone(status: AuctionStatus): 'seal' | 'moss' | 'faint' {
  return status === 'active' ? 'seal' : status === 'sold' ? 'moss' : 'faint'
}

export function listingItem(l: AuctionListing): InventoryItem {
  return { name: l.item_name, desc: l.item_desc || undefined, count: l.count, type: l.item_type }
}

export function listingLabel(l: AuctionListing): string {
  return itemLabel(listingItem(l))
}

export interface BrowseFilter {
  q: string
  type: string
  sort: AuctionSort
  page: number
  pageSize: number
}

export function browseQuery(f: BrowseFilter): string {
  const params = new URLSearchParams({ page: String(f.page), page_size: String(f.pageSize), sort: f.sort })
  const q = f.q.trim()
  if (q) params.set('q', q)
  if (f.type) params.set('type', f.type)
  return params.toString()
}

// NOTE: 返回不能购买的原因，可以购买时返回 null；服务端对这两条同样会校验，这里只是提前置灰按钮
export function buyBlocker(l: AuctionListing, userId: number | undefined, coins: number): string | null {
  if (userId != null && l.seller_id === userId) return '这是你自己的挂单'
  if (coins < l.price) return '金币不足'
  return null
}

export type ListingPlan = { ok: true; count: number; price: number; confirmMessage: string } | { ok: false; error: string }

// NOTE: 先校验再确认，避免用户确认完才被服务端拒绝；数量和价格都必须是整数，价格不设上限
export function planListing(item: InventoryItem | null, countRaw: unknown, priceRaw: unknown): ListingPlan {
  if (!item) return { ok: false, error: '请选择要上架的物品' }
  const count = Number(countRaw)
  if (countRaw === '' || !Number.isInteger(count) || count < 1) return { ok: false, error: '数量必须是正整数' }
  if (count > item.count) return { ok: false, error: `仓库里只有 ${item.count} 件` }
  const price = Number(priceRaw)
  if (priceRaw === '' || !Number.isSafeInteger(price) || price < 1) return { ok: false, error: '价格必须是正整数' }
  return {
    ok: true,
    count,
    price,
    confirmMessage: `确认以 ${price} 金币上架「${itemLabel({ ...item, count })}」？\n上架后物品会从仓库移出，下架后退回仓库。`,
  }
}
