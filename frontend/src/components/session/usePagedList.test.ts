// @vitest-environment jsdom
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { usePagedList } from './usePagedList'

const apiMock = vi.hoisted(() => vi.fn())
vi.mock('@/api/client', async (orig) => ({ ...(await orig<typeof import('@/api/client')>()), api: apiMock }))

// 响应信封：服务端字段缺省时用调用方的页码兜底
const envelope = (items: number[], over: Record<string, unknown> = {}) => ({ items, page: 1, page_size: 20, total: items.length, total_pages: 1, ...over })

beforeEach(() => {
  setActivePinia(createPinia())
  apiMock.mockReset()
})

describe('usePagedList', () => {
  test('请求带页码和页大小；响应里的分页信息覆盖本地值', async () => {
    apiMock.mockResolvedValue(envelope([1, 2], { page: 2, page_size: 10, total: 25, total_pages: 3 }))
    const list = usePagedList<number>('/api/sessions')
    await list.load(2)
    expect(apiMock).toHaveBeenCalledWith('GET', '/api/sessions?page=2&page_size=20')
    expect(list.items.value).toEqual([1, 2])
    expect(list.page.value).toBe(2)
    expect(list.pageSize.value).toBe(10)
    expect(list.total.value).toBe(25)
    expect(list.totalPages.value).toBe(3)
    expect(list.loading.value).toBe(false)
  })

  test('不带页码时重载当前页', async () => {
    apiMock.mockResolvedValue(envelope([1], { page: 3, total_pages: 3 }))
    const list = usePagedList<number>('/api/sessions')
    await list.load(3)
    await list.load()
    expect(apiMock).toHaveBeenLastCalledWith('GET', '/api/sessions?page=3&page_size=20')
  })

  test('响应缺字段时按安全默认值处理', async () => {
    apiMock.mockResolvedValue({})
    const list = usePagedList<number>('/api/sessions')
    await list.load()
    expect(list.items.value).toEqual([])
    expect(list.page.value).toBe(1)
    expect(list.total.value).toBe(0)
    expect(list.totalPages.value).toBe(1)
  })

  test('prev/next 在边界上不发请求', async () => {
    apiMock.mockResolvedValue(envelope([1], { page: 1, total_pages: 2 }))
    const list = usePagedList<number>('/api/sessions')
    await list.load(1)
    apiMock.mockClear()
    await list.prev()
    expect(apiMock).not.toHaveBeenCalled()

    apiMock.mockResolvedValue(envelope([2], { page: 2, total_pages: 2 }))
    await list.next()
    expect(apiMock).toHaveBeenCalledWith('GET', '/api/sessions?page=2&page_size=20')
    apiMock.mockClear()
    await list.next()
    expect(apiMock).not.toHaveBeenCalled()
  })

  test('请求失败时抛出，并复位加载状态、保留旧数据', async () => {
    apiMock.mockResolvedValueOnce(envelope([1]))
    const list = usePagedList<number>('/api/sessions')
    await list.load(1)
    apiMock.mockRejectedValueOnce(new ApiError('服务器错误', 500))
    await expect(list.load(1)).rejects.toThrow('服务器错误')
    expect(list.loading.value).toBe(false)
    expect(list.items.value).toEqual([1])
  })

  test('连续翻页时只采纳最后一次请求的结果', async () => {
    let resolveSlow!: (v: unknown) => void
    apiMock.mockImplementationOnce(() => new Promise((r) => (resolveSlow = r)))
    apiMock.mockResolvedValueOnce(envelope([20], { page: 2, total_pages: 2 }))
    const list = usePagedList<number>('/api/sessions')
    const slow = list.load(1)
    await list.load(2)
    resolveSlow(envelope([10], { page: 1, total_pages: 2 }))
    await slow
    expect(list.items.value).toEqual([20])
    expect(list.page.value).toBe(2)
    expect(list.loading.value).toBe(false)
  })

  test('startLoading 让首帧处于加载态；prepend 插到最前并累加总数', async () => {
    const list = usePagedList<number>('/api/sessions', 20, true)
    expect(list.loading.value).toBe(true)
    apiMock.mockResolvedValue(envelope([2, 3]))
    await list.load()
    list.prepend(1)
    expect(list.items.value).toEqual([1, 2, 3])
    expect(list.total.value).toBe(3)
  })
})
