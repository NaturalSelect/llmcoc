import { shallowRef, ref } from 'vue'
import { api } from '@/api/client'
import type { AdminPaged } from '@/api/adminTypesContent'

// NOTE: 模组/商城/公告/邀请码四个列表共用的分页状态，逻辑与旧 admin.js 的 loadAdminXxx/setAdminXxxPage 一一对应。
// 这四个页签的共享代码只能放在自己名下的目录里，所以统一放在 scenarios/，其余三个页签从这里引用

export const PAGE_SIZES = [10, 20, 50] as const

export interface PagedView<T> {
  items: T[]
  page: number
  pageSize: number
  total: number
  totalPages: number
}

export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

// NOTE: 兼容两种响应：分页信封，或老接口直接返回数组（此时视为只有一页）
export function normalizePaged<T>(resp: AdminPaged<T> | T[] | null | undefined, requestedPage: number, pageSize: number): PagedView<T> {
  if (Array.isArray(resp)) {
    return { items: resp, page: requestedPage, pageSize, total: resp.length, totalPages: 1 }
  }
  return {
    items: resp?.items || [],
    page: Math.max(1, Number(resp?.page || requestedPage)),
    pageSize: Math.max(1, Number(resp?.page_size || pageSize)),
    total: Math.max(0, Number(resp?.total || 0)),
    totalPages: Math.max(1, Number(resp?.total_pages || 1)),
  }
}

export function clampPage(page: number, totalPages: number): number {
  return Math.min(Math.max(1, Number(page || 1)), Math.max(1, Number(totalPages || 1)))
}

// NOTE: 删掉当前页最后一条后回退一页，避免停在空页
export function pageAfterDelete(itemCount: number, page: number): number {
  return itemCount === 1 && page > 1 ? page - 1 : page
}

export function sanitizePageSize(v: number): number {
  const n = Number(v || 20)
  return (PAGE_SIZES as readonly number[]).includes(n) ? n : 20
}

export function usePagedList<T>(basePath: string, initialPageSize = 20) {
  const items = shallowRef<T[]>([])
  const page = ref(1)
  const pageSize = ref(initialPageSize)
  const total = ref(0)
  const totalPages = ref(1)
  // NOTE: 快速连点翻页时较早发出的请求可能后返回，只采用最后一次请求的结果
  let latest = 0

  async function load(p?: number): Promise<void> {
    const nextPage = Math.max(1, Number(p || page.value || 1))
    const size = Math.max(1, Number(pageSize.value || 20))
    const seq = ++latest
    const resp = await api<AdminPaged<T> | T[]>(
      'GET',
      `${basePath}?page=${encodeURIComponent(nextPage)}&page_size=${encodeURIComponent(size)}`,
    )
    if (seq !== latest) return
    const view = normalizePaged(resp, nextPage, size)
    items.value = view.items
    page.value = view.page
    pageSize.value = view.pageSize
    total.value = view.total
    totalPages.value = view.totalPages
  }

  const setPage = (p: number) => load(clampPage(p, totalPages.value))
  const prev = () => setPage(page.value - 1)
  const next = () => setPage(page.value + 1)
  const reloadAfterDelete = () => load(pageAfterDelete(items.value.length, page.value))

  async function changePageSize(): Promise<void> {
    pageSize.value = sanitizePageSize(pageSize.value)
    await load(1)
  }

  return { items, page, pageSize, total, totalPages, load, setPage, prev, next, reloadAfterDelete, changePageSize }
}
