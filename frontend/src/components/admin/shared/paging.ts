// NOTE: 用户列表与缓存条目列表共用的分页归一化，行为与旧 admin.js 的 loadAdminUsers / loadCacheKeys 一致

export interface PageState<T> {
  items: T[]
  page: number
  pageSize: number
  total: number
  totalPages: number
}

export const PAGE_SIZE_OPTIONS = [10, 20, 50] as const

export function clampPage(page: unknown, totalPages: unknown): number {
  const max = Math.max(1, Number(totalPages || 1))
  return Math.min(Math.max(1, Number(page || 1)), max)
}

// NOTE: 每页条数只接受预设值，其它回落 20
export function sanitizePageSize(size: unknown): number {
  const n = Number(size || 20)
  return (PAGE_SIZE_OPTIONS as readonly number[]).includes(n) ? n : 20
}

// NOTE: 兼容后端早期直接返回数组的形态（旧前端同样兼容）
export function normalizeItemsPage<T>(resp: unknown, reqPage: number, reqSize: number): PageState<T> {
  if (Array.isArray(resp)) {
    return { items: resp as T[], page: reqPage, pageSize: reqSize, total: resp.length, totalPages: 1 }
  }
  const r = (resp ?? {}) as Record<string, unknown>
  return {
    items: (r.items as T[] | undefined) || [],
    page: Math.max(1, Number(r.page || reqPage)),
    pageSize: Math.max(1, Number(r.page_size || reqSize)),
    total: Math.max(0, Number(r.total || 0)),
    totalPages: Math.max(1, Number(r.total_pages || 1)),
  }
}

export function normalizeKeysPage(resp: unknown, reqPage: number, reqSize: number): PageState<string> {
  if (Array.isArray(resp)) {
    const keys = ([...resp] as string[]).sort()
    return { items: keys, page: 1, pageSize: reqSize, total: keys.length, totalPages: 1 }
  }
  const r = (resp ?? {}) as Record<string, unknown>
  return {
    items: (r.keys as string[] | undefined) || [],
    page: Math.max(1, Number(r.page || reqPage)),
    pageSize: Math.max(1, Number(r.page_size || reqSize)),
    total: Math.max(0, Number(r.total || 0)),
    totalPages: Math.max(1, Number(r.total_pages || 1)),
  }
}
