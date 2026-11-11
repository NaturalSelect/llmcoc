import type { Scenario } from './scenarioTypes'
import type { ShopItem } from './shopTypes'

// NOTE: 后端统一分页信封（handlers/pagination.go）。旧前端还兼容过接口直接返回数组的情况，解析见 normalizePaged
export interface AdminPaged<T> {
  items: T[]
  page: number
  page_size: number
  total: number
  total_pages: number
}

export type AdminScenario = Scenario
export type AdminShopItem = ShopItem

export interface ScenarioGenerationLog {
  scenario_id: number
  scenario_name: string
  has_log: boolean
  log_text: string
  has_story: boolean
  story_document: string
  created_at?: string
  updated_at?: string
}

// NOTE: 数字输入框清空后 v-model.number 会给出空字符串，所以数值字段允许 string；提交时统一 Number(x || 默认值)
export interface ScenarioGenForm {
  name: string
  theme: string
  era: string
  brief: string
  target_length: string
  min_players: number | string
  max_players: number | string
  difficulty: string
  count: number | string
}

export interface GenProgressInfo {
  stage?: string
  status?: string
  detail?: string
}

export interface BatchProgressInfo {
  current: number
  total: number
}

// NOTE: done 事件在模组生成与故事编译两条流里共用
export interface GenDoneInfo {
  index?: number
  scenario_id?: number
  name?: string
}

export interface GenErrorInfo {
  index?: number
  message?: string
}

export interface BatchResult {
  index: number
  scenario_id?: number
  name?: string
  error?: string
}

export interface BatchDone {
  total: number
  succeeded: number
  failed: number
  results?: BatchResult[]
}

export interface ShopItemForm {
  name: string
  description: string
  item_type: string
  price: number | string
  value: number | string
  is_active: boolean
}

export interface Announcement {
  id: number
  title: string
  content: string
  level: string
  is_pinned: boolean
  is_active: boolean
  starts_at: string | null
  ends_at: string | null
  created_by?: number
  created_at: string
  updated_at?: string
}

// NOTE: starts_at / ends_at 在表单里是 datetime-local 的本地时间字符串，提交时才转 ISO
export interface AnnouncementForm {
  title: string
  content: string
  level: string
  is_pinned: boolean
  is_active: boolean
  starts_at: string
  ends_at: string
}

export interface AnnouncementPayload extends Omit<AnnouncementForm, 'starts_at' | 'ends_at'> {
  starts_at: string | null
  ends_at: string | null
}

export interface SiteSettingRow {
  key: string
  value: string
}

export interface SiteSettings {
  require_invite_code: boolean
  allow_nsfw: boolean
  initial_coins: number
  initial_card_slots: number
  regenerate_appearance_cost: number
  regenerate_backstory_cost: number
  regenerate_traits_cost: number
  regenerate_portrait_cost: number
  revive_base_cost: number
  max_character_drafts: number
  end_session_cost: number
  balance_rules: string
}

export interface InviteCode {
  id: number
  code: string
  used_by: number | null
  used_at?: string | null
  created_at: string
  used_user?: { username?: string } | null
}
