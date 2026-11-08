// @vitest-environment node
import { describe, expect, test } from 'vitest'
import { ADMIN_TABS, DEFAULT_ADMIN_TAB, resolveAdminTab } from './adminTabs'

describe('adminTabs', () => {
  test('八个 tab，key 不重复，与旧后台一致', () => {
    expect(ADMIN_TABS.map((t) => t.label)).toStrictEqual([
      '用户',
      'LLM 提供商',
      'Agent 配置',
      '模组管理',
      '商城管理',
      '公告管理',
      '系统设置',
      '规则缓存',
    ])
    expect(new Set(ADMIN_TABS.map((t) => t.key)).size).toBe(8)
  })

  test('resolveAdminTab：合法值原样返回，其它回落到默认 tab', () => {
    expect(resolveAdminTab('cache')).toBe('cache')
    expect(resolveAdminTab(['shop', 'x'])).toBe('shop')
    expect(resolveAdminTab(undefined)).toBe(DEFAULT_ADMIN_TAB)
    expect(resolveAdminTab('')).toBe(DEFAULT_ADMIN_TAB)
    expect(resolveAdminTab('nope')).toBe(DEFAULT_ADMIN_TAB)
    expect(resolveAdminTab([])).toBe(DEFAULT_ADMIN_TAB)
  })
})
