export const ADMIN_TABS = [
  { key: 'users', label: '用户' },
  { key: 'providers', label: 'LLM 提供商' },
  { key: 'agents', label: 'Agent 配置' },
  { key: 'scenarios', label: '模组管理' },
  { key: 'online', label: '线上模组' },
  { key: 'shop', label: '商城管理' },
  { key: 'announcements', label: '公告管理' },
  { key: 'settings', label: '系统设置' },
  { key: 'cache', label: '规则缓存' },
] as const

export type AdminTabKey = (typeof ADMIN_TABS)[number]['key']

export const DEFAULT_ADMIN_TAB: AdminTabKey = 'users'

// NOTE: 路由参数来自地址栏，可能是缺省、数组或不存在的 tab；一律回落到默认 tab
export function resolveAdminTab(raw: unknown): AdminTabKey {
  const v = Array.isArray(raw) ? raw[0] : raw
  return ADMIN_TABS.find((t) => t.key === v)?.key ?? DEFAULT_ADMIN_TAB
}
