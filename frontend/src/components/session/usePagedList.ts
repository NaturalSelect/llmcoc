import { ref, shallowRef } from 'vue'
import { api } from '@/api/client'
import type { PagedResponse } from '@/api/sessionTypes'

// NOTE: 跑团大厅有四个分页列表（活跃房间/历史/收藏/选剧本），翻页语义完全一致，统一在这里实现。
// load() 不带页码时重载当前页；服务端返回的 page/page_size 优先于请求值。
// startLoading 用于挂载后马上拉取的列表，避免首帧闪现空状态
export function usePagedList<T>(path: string, initialPageSize = 20, startLoading = false) {
  const items = shallowRef<T[]>([])
  const page = ref(1)
  const pageSize = ref(initialPageSize)
  const total = ref(0)
  const totalPages = ref(1)
  const loading = ref(startLoading)
  // NOTE: 连续点翻页时后发的请求可能先返回；只采纳最近一次请求的结果
  let seq = 0

  async function load(target?: number): Promise<void> {
    const mine = ++seq
    const next = Math.max(1, Number(target || page.value || 1))
    const size = Math.max(1, Number(pageSize.value || 20))
    loading.value = true
    try {
      const resp = await api<PagedResponse<T>>('GET', `${path}?page=${encodeURIComponent(next)}&page_size=${encodeURIComponent(size)}`)
      if (mine !== seq) return
      items.value = resp?.items || []
      page.value = Math.max(1, Number(resp?.page || next))
      pageSize.value = Math.max(1, Number(resp?.page_size || size))
      total.value = Math.max(0, Number(resp?.total || 0))
      totalPages.value = Math.max(1, Number(resp?.total_pages || 1))
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  async function prev(): Promise<void> {
    if (page.value <= 1) return
    await load(page.value - 1)
  }

  async function next(): Promise<void> {
    if (page.value >= totalPages.value) return
    await load(page.value + 1)
  }

  // 新建房间后直接插到列表最前面，不重拉；总数同步 +1
  function prepend(item: T): void {
    items.value = [item, ...items.value]
    total.value += 1
  }

  return { items, page, pageSize, total, totalPages, loading, load, prev, next, prepend }
}
