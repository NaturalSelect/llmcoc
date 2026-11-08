// @vitest-environment jsdom
import { afterEach, describe, expect, test, vi } from 'vitest'
import { clampPage, normalizePaged, pageAfterDelete, sanitizePageSize, usePagedList } from './pagedList'

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

describe('normalizePaged', () => {
  test('分页信封：字段缺失时回落到请求参数，下限夹紧', () => {
    expect(normalizePaged({ items: [1, 2], page: 2, page_size: 10, total: 25, total_pages: 3 }, 1, 20)).toStrictEqual({
      items: [1, 2],
      page: 2,
      pageSize: 10,
      total: 25,
      totalPages: 3,
    })
    expect(normalizePaged({ items: [] } as never, 4, 20)).toStrictEqual({ items: [], page: 4, pageSize: 20, total: 0, totalPages: 1 })
    expect(normalizePaged(null, 1, 20).items).toStrictEqual([])
  })

  test('老接口直接返回数组：视为只有一页', () => {
    expect(normalizePaged([1, 2, 3], 3, 50)).toStrictEqual({ items: [1, 2, 3], page: 3, pageSize: 50, total: 3, totalPages: 1 })
  })
})

describe('页码辅助', () => {
  test('clampPage 夹在 1..totalPages', () => {
    expect(clampPage(0, 5)).toBe(1)
    expect(clampPage(9, 5)).toBe(5)
    expect(clampPage(3, 5)).toBe(3)
    expect(clampPage(2, 0)).toBe(1)
  })

  test('pageAfterDelete：删掉当前页最后一条且不在第 1 页时回退一页', () => {
    expect(pageAfterDelete(1, 3)).toBe(2)
    expect(pageAfterDelete(1, 1)).toBe(1)
    expect(pageAfterDelete(5, 3)).toBe(3)
  })

  test('sanitizePageSize 只允许 10 / 20 / 50，其余回落 20', () => {
    expect(sanitizePageSize(10)).toBe(10)
    expect(sanitizePageSize(50)).toBe(50)
    expect(sanitizePageSize(33)).toBe(20)
    expect(sanitizePageSize(0)).toBe(20)
  })
})

function stubApi(handler: (url: string) => unknown) {
  const calls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      calls.push(url)
      return { ok: true, status: 200, json: async () => handler(url) } as Response
    }),
  )
  return calls
}

describe('usePagedList', () => {
  test('load 带 page / page_size 查询串并写入状态', async () => {
    const calls = stubApi(() => ({ items: [{ id: 1 }], page: 1, page_size: 20, total: 45, total_pages: 3 }))
    const list = usePagedList<{ id: number }>('/api/admin/scenarios')
    await list.load()
    expect(calls).toStrictEqual(['/api/admin/scenarios?page=1&page_size=20'])
    expect(list.items.value).toStrictEqual([{ id: 1 }])
    expect([list.total.value, list.totalPages.value]).toStrictEqual([45, 3])
  })

  test('翻页被夹在范围内；改每页条数回到第 1 页', async () => {
    const calls = stubApi((url) => {
      const page = Number(new URL(url, 'http://x').searchParams.get('page'))
      const size = Number(new URL(url, 'http://x').searchParams.get('page_size'))
      return { items: [], page, page_size: size, total: 45, total_pages: 3 }
    })
    const list = usePagedList('/p')
    await list.load()
    await list.next()
    await list.next()
    await list.next()
    expect(list.page.value).toBe(3)
    await list.prev()
    expect(list.page.value).toBe(2)
    list.pageSize.value = 50
    await list.changePageSize()
    expect(calls.at(-1)).toBe('/p?page=1&page_size=50')
    list.pageSize.value = 33
    await list.changePageSize()
    expect(calls.at(-1)).toBe('/p?page=1&page_size=20')
  })

  test('reloadAfterDelete：当前页只剩一条且不在第 1 页时请求上一页', async () => {
    const calls = stubApi(() => ({ items: [{ id: 9 }], page: 2, page_size: 20, total: 21, total_pages: 2 }))
    const list = usePagedList<{ id: number }>('/p')
    await list.load(2)
    await list.reloadAfterDelete()
    expect(calls.at(-1)).toBe('/p?page=1&page_size=20')
  })

  test('较早发出的请求后返回时被丢弃，只采用最后一次请求的结果', async () => {
    const resolvers: Array<(v: unknown) => void> = []
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise((resolve) => {
            resolvers.push((body) => resolve({ ok: true, status: 200, json: async () => body } as Response))
          }),
      ),
    )
    const list = usePagedList<{ id: number }>('/p')
    const first = list.load(1)
    const second = list.load(2)
    resolvers[1]({ items: [{ id: 2 }], page: 2, page_size: 20, total: 40, total_pages: 2 })
    await second
    resolvers[0]({ items: [{ id: 1 }], page: 1, page_size: 20, total: 40, total_pages: 2 })
    await first
    expect(list.items.value).toStrictEqual([{ id: 2 }])
    expect(list.page.value).toBe(2)
  })
})
