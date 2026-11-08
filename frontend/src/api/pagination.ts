import { api } from './client'

export interface Page<T> {
  items: T[]
  total?: number
  total_pages?: number
  page?: number
}

export interface FetchAllOptions<T> {
  pageSize?: number
  delayMs?: number
  onProgress?: (all: T[]) => void
}

// NOTE: 渐进式拉取某个分页接口的全部数据。
// 用于下拉选卡等必须拿到完整数据集的场景（如人物卡列表被加入房间/商城复活/管理员加物品等多处直接复用），
// 不能只拉一页；每页之间加短间隔避免连续请求压垮后端。onProgress 可用于边拉边刷新界面。
export async function fetchAllPages<T = any>(
  path: string,
  { pageSize = 100, delayMs = 120, onProgress }: FetchAllOptions<T> = {},
): Promise<T[]> {
  const sep = path.includes('?') ? '&' : '?'
  let page = 1
  let all: T[] = []
  while (true) {
    const resp = await api<Page<T>>('GET', `${path}${sep}page=${page}&page_size=${pageSize}`)
    all = all.concat(resp?.items || [])
    onProgress?.(all.slice())
    const totalPages = Math.max(1, Number(resp?.total_pages || 1))
    if (page >= totalPages) break
    page += 1
    await new Promise((r) => setTimeout(r, delayMs))
  }
  return all
}
