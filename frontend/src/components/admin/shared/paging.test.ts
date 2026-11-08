import { describe, expect, test } from 'vitest'
import { clampPage, normalizeItemsPage, normalizeKeysPage, sanitizePageSize } from './paging'

describe('clampPage', () => {
  test('夹在 1 到总页数之间，非法值回落到 1', () => {
    expect(clampPage(0, 5)).toBe(1)
    expect(clampPage(-3, 5)).toBe(1)
    expect(clampPage(9, 5)).toBe(5)
    expect(clampPage(undefined, 5)).toBe(1)
    expect(clampPage(3, 0)).toBe(1)
    expect(clampPage(3, 5)).toBe(3)
  })
})

describe('sanitizePageSize', () => {
  test('只接受 10/20/50，其它回落 20', () => {
    expect(sanitizePageSize(10)).toBe(10)
    expect(sanitizePageSize('50')).toBe(50)
    expect(sanitizePageSize(33)).toBe(20)
    expect(sanitizePageSize(undefined)).toBe(20)
    expect(sanitizePageSize(0)).toBe(20)
  })
})

describe('normalizeItemsPage', () => {
  test('标准分页信封', () => {
    const r = normalizeItemsPage<{ id: number }>({ items: [{ id: 1 }], page: 2, page_size: 10, total: 25, total_pages: 3 }, 1, 20)
    expect(r).toStrictEqual({ items: [{ id: 1 }], page: 2, pageSize: 10, total: 25, totalPages: 3 })
  })

  test('旧后端直接返回数组：单页，total 取长度', () => {
    const r = normalizeItemsPage<number>([1, 2, 3], 4, 20)
    expect(r).toStrictEqual({ items: [1, 2, 3], page: 4, pageSize: 20, total: 3, totalPages: 1 })
  })

  test('字段缺失时回落到请求参数与下限', () => {
    expect(normalizeItemsPage(null, 2, 20)).toStrictEqual({ items: [], page: 2, pageSize: 20, total: 0, totalPages: 1 })
    expect(normalizeItemsPage({ items: null, total_pages: 0, total: -4 }, 1, 10)).toStrictEqual({
      items: [],
      page: 1,
      pageSize: 10,
      total: 0,
      totalPages: 1,
    })
  })
})

describe('normalizeKeysPage', () => {
  test('标准形态读取 keys', () => {
    const r = normalizeKeysPage({ keys: ['a', 'b'], page: 1, page_size: 20, total: 2, total_pages: 1 }, 1, 20)
    expect(r).toStrictEqual({ items: ['a', 'b'], page: 1, pageSize: 20, total: 2, totalPages: 1 })
  })

  test('数组形态排序且回到第 1 页，且不修改入参', () => {
    const src = ['b', 'a', 'c']
    const r = normalizeKeysPage(src, 3, 20)
    expect(r.items).toStrictEqual(['a', 'b', 'c'])
    expect(src).toStrictEqual(['b', 'a', 'c'])
    expect(r.page).toBe(1)
    expect(r.total).toBe(3)
  })

  test('keys 为 null 时视为空列表', () => {
    expect(normalizeKeysPage({ keys: null, total: 0 }, 2, 20).items).toStrictEqual([])
  })
})
