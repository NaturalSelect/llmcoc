import type { InventoryItem } from './characterTypes'

// NOTE: 只覆盖后台「用户 / LLM 提供商 / Agent 配置 / 规则缓存」四个 tab 的 JSON；
// 字段对照 internal/handlers/admin*.go 与 internal/models/models.go 的 json 标签

export interface AdminPageEnvelope<T> {
  items: T[]
  page: number
  page_size: number
  total: number
  total_pages: number
}

export interface AdminUser {
  id: number
  username: string
  email: string
  role: string
  is_banned: boolean
  ban_reason?: string
  coins: number
  card_slots?: number
  revive_count?: number
  created_at?: string
  updated_at?: string
}

// NOTE: 表单值与旧前端一致：数字输入清空后是空串，原样提交，由后端校验并返回错误文案
export interface RechargeForm {
  user_id: number | ''
  amount: number | ''
  note: string
}

export interface RechargeResult {
  message: string
  user: AdminUser
}

// NOTE: 加物品弹窗只用到这几个字段；inventory 在后端可能序列化为 null
export interface AdminCharacterCard {
  id: number
  name: string
  occupation?: string
  inventory?: InventoryItem[] | null
  [key: string]: unknown
}

export interface ProviderConfig {
  id: number
  name: string
  provider: string
  base_url: string
  api_key_set: boolean
  is_active: boolean
}

export interface ProviderForm {
  name: string
  provider: string
  base_url: string
  api_key: string
  is_active: boolean
}

export interface AgentConfigRow {
  id: number
  role: string
  provider_config_id: number | null
  model_name: string
  max_tokens: number
  temperature: number
  disable_temperature: boolean
  image_via_chat: boolean
  diffusion_mode: boolean
  context_window: number
  with_jailbreak: boolean
  system_prompt: string
  thinking_level: string
  thinking_budget_tokens: number
  is_active: boolean
  provider_config?: unknown
}

// NOTE: 表单态：库里缺行的角色用 'new-<role>' 作 id，数字输入清空后为空串
export interface AgentFormState {
  id: number | string
  role: string
  provider_config_id: number | ''
  model_name: string
  max_tokens: number | ''
  context_window: number | ''
  temperature: number | ''
  disable_temperature: boolean
  image_via_chat: boolean
  diffusion_mode: boolean
  with_jailbreak: boolean
  thinking_level: string
  thinking_budget_tokens: number | ''
  system_prompt: string
  is_active: boolean
}

export interface PingResult {
  ok: boolean
  mode: string
  latency_ms: number
}

export interface CacheStats {
  full_hits: number
  partial_hits: number
  misses: number
  entries: number
  used_bytes: number
  max_bytes: number
}

export interface CacheKeysPage {
  keys: string[]
  total: number
  page: number
  page_size: number
  total_pages: number
}

export interface CacheEntry {
  key: string
  value: string
  size?: number
}

export interface LlmStatLine {
  key: string
  count: number
  avg_ms: number
  max_ms: number
  err_count: number
  prompt_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_creation_tokens: number
  cache_hit_rate: number
}

export interface LlmStats {
  overall?: LlmStatLine
  by_role?: LlmStatLine[] | null
  by_model?: LlmStatLine[] | null
}

export interface TurnCacheStat {
  seq: number
  round: number
  calls: number
  prompt_tokens: number
  cache_read_tokens: number
  cache_creation_tokens?: number
  output_tokens?: number
  trimmed_turns?: number
  compacted_turns?: number
}

export interface SessionContextAgent {
  agent_key: string
  turns: number
  next_seq: number
  estimated_runes: number
  // NOTE: Go 的空切片可能序列化为 null
  recent_stats?: TurnCacheStat[] | null
  updated_at?: string
}

export interface SessionContext {
  agents: SessionContextAgent[]
}
