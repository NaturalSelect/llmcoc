import type { SiteSettingRow, SiteSettings } from '@/api/adminTypesContent'

export const BALANCE_RULES_MAX = 2000

// NOTE: 每项都有独立的「保存」按钮，顺序与文案跟旧页面保持一致
export const COST_FIELDS = [
  { key: 'initial_coins', label: '初始金币（新用户注册）', min: 0 },
  { key: 'initial_card_slots', label: '初始卡槽数（新用户注册）', min: 0 },
  { key: 'regenerate_appearance_cost', label: '重造外貌费用', min: 0 },
  { key: 'regenerate_backstory_cost', label: '重造背景故事费用', min: 0 },
  { key: 'regenerate_traits_cost', label: '重造性格特征费用', min: 0 },
  { key: 'revive_base_cost', label: '复活基础费用', min: 0 },
  { key: 'max_character_drafts', label: '角色草稿最大数量', min: 1 },
  { key: 'end_session_cost', label: '结束游戏费用（每人）', min: 0 },
] as const

export type CostKey = (typeof COST_FIELDS)[number]['key']

export const DEFAULT_SITE_SETTINGS: SiteSettings = {
  require_invite_code: false,
  allow_nsfw: true,
  initial_coins: 600,
  initial_card_slots: 3,
  regenerate_appearance_cost: 100,
  regenerate_backstory_cost: 100,
  regenerate_traits_cost: 100,
  revive_base_cost: 2000,
  max_character_drafts: 3,
  end_session_cost: 200,
  balance_rules: '',
}

// NOTE: 旧实现写作 parseInt(x) || 默认值，库里存 0 时会被误显示成默认值（如初始金币 0 显示 600），
// 这里只在缺失或无法解析时才用默认值，0 按原值显示
function intOr(raw: string | undefined, fallback: number): number {
  const n = parseInt(raw ?? '', 10)
  return Number.isNaN(n) ? fallback : n
}

export function parseSiteSettings(rows: SiteSettingRow[] | null | undefined): SiteSettings {
  const map: Record<string, string> = {}
  for (const r of rows || []) map[r.key] = r.value
  const d = DEFAULT_SITE_SETTINGS
  return {
    require_invite_code: map.require_invite_code === 'true',
    // NOTE: 只有显式存成 'false' 才算关闭，缺失按开启处理
    allow_nsfw: map.allow_nsfw !== 'false',
    initial_coins: intOr(map.initial_coins, d.initial_coins),
    initial_card_slots: intOr(map.initial_card_slots, d.initial_card_slots),
    regenerate_appearance_cost: intOr(map.regenerate_appearance_cost, d.regenerate_appearance_cost),
    regenerate_backstory_cost: intOr(map.regenerate_backstory_cost, d.regenerate_backstory_cost),
    regenerate_traits_cost: intOr(map.regenerate_traits_cost, d.regenerate_traits_cost),
    revive_base_cost: intOr(map.revive_base_cost, d.revive_base_cost),
    max_character_drafts: intOr(map.max_character_drafts, d.max_character_drafts),
    end_session_cost: intOr(map.end_session_cost, d.end_session_cost),
    balance_rules: map.balance_rules !== undefined ? map.balance_rules : '',
  }
}

// NOTE: 沿用旧校验：空值、无法解析、负数都拒绝；提交的是输入框的原始字符串（不取整），与旧实现一致。
// 返回要提交的字符串，非法时返回 null
export function costValueToSubmit(raw: unknown): string | null {
  const val = String(raw ?? '')
  if (!val || Number.isNaN(parseInt(val, 10)) || parseInt(val, 10) < 0) return null
  return val
}

// NOTE: 按 Unicode 字符计数（emoji 等代理对算一个），与后端按 rune 计数的 2000 上限一致
export function balanceRulesLength(s: string | null | undefined): number {
  return [...(s || '')].length
}
