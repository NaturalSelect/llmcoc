// @vitest-environment node
import { describe, expect, test } from 'vitest'
import { ADMIN_TABS, DEFAULT_ADMIN_TAB, resolveAdminTab } from './adminTabs'

describe('adminTabs', () => {
  test('九个 tab，key 不重复，线上模组紧跟在模组管理后面', () => {
    expect(ADMIN_TABS.map((t) => t.label)).toStrictEqual([
      '用户',
      'LLM 提供商',
      'Agent 配置',
      '模组管理',
      '线上模组',
      '商城管理',
      '公告管理',
      '系统设置',
      '规则缓存',
    ])
    expect(new Set(ADMIN_TABS.map((t) => t.key)).size).toBe(9)
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
