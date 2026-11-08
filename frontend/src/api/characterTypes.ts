import type { CardStats, CharacterCard } from './types'

// NOTE: 字段按 internal/models/models.go 的 CharacterCard / CharacterStats JSON 标签整理；
// 后端 JSONField 的空切片可能序列化成 null，所以列表字段都带 null

export interface CharacterStatsFull extends CardStats {
  str?: number
  con?: number
  siz?: number
  dex?: number
  app?: number
  int?: number
  pow?: number
  edu?: number
  hp?: number
  max_hp?: number
  mp?: number
  max_mp?: number
  san?: number
  max_san?: number
  luck?: number
  mov?: number
  build?: number
  db?: string
}

export interface SocialRelation {
  name?: string
  relationship?: string
  note?: string
}

export interface CharacterAsset {
  name?: string
  category?: string
  note?: string
}

export interface CharacterCardFull extends CharacterCard {
  user_id?: number
  race?: string
  age?: number
  gender?: string
  occupation?: string
  birthplace?: string
  residence?: string
  stats?: CharacterStatsFull
  skills?: Record<string, number> | null
  backstory?: string
  appearance?: string
  traits?: string
  inventory?: string[] | null
  social_relations?: SocialRelation[] | null
  assets?: CharacterAsset[] | null
  spells?: string[] | null
  seen_monsters?: string[] | null
  is_active?: boolean
  avatar_url?: string
  madness_state?: string
  madness_symptom?: string
  madness_duration?: number
  daily_san_loss?: number
  cthulhu_mythos_skill?: number
  wound_state?: string
  is_unconscious?: boolean
  in_session?: boolean
  created_at?: string
  updated_at?: string
}

export interface SkillBudget {
  occupation: number
  interest: number
  total: number
}

export interface RollDraft {
  draft_id: number
  token: string
  stats: CharacterStatsFull
  raw_rolls?: { age_log?: string[] | null; [key: string]: unknown }
  skill_defaults?: Record<string, number>
  skill_budget?: SkillBudget
  expires_at?: string
}

export interface SkillDefaultsResponse {
  skill_defaults?: Record<string, number>
  skill_names?: string[]
  locked_skills?: string[]
  blocked_skills?: string[]
  max_skill?: number
}

export interface FinalizePayload {
  draft_id: number
  token: string
  name: string
  gender: string
  occupation: string
  birthplace: string
  residence: string
  creation_hint: string
  skills: Record<string, number>
}

export interface GeneratePayload {
  name: string
  race: string
  gender: string
  // NOTE: 留空时不发送，服务端会把缺省年龄钳到 15；旧前端发的是空字符串，会触发 JSON 反序列化失败
  age?: number
  occupation: string
  era: string
  background: string
}

export type RegenField = 'appearance' | 'backstory' | 'traits'

// NOTE: regenerate-{appearance,backstory,traits} 的响应；非本人（管理员）操作时没有 coins
export interface RegenResponse {
  appearance?: string
  backstory?: string
  traits?: string
  coins?: number
}

// NOTE: 管理员复活别人的卡时没有 coins
export interface ReviveResponse {
  message?: string
  character_card: CharacterCardFull
  coins?: number
  revive_count?: number
}

export interface WarehouseResponse {
  warehouse?: string[] | null
}

export interface WarehouseTransferResponse extends WarehouseResponse {
  message?: string
  character_card: CharacterCardFull
}

export interface Announcement {
  id: number
  title: string
  content: string
  level: string
  is_pinned: boolean
  starts_at?: string | null
  created_at?: string
}

export interface RecentSession {
  id: number
  name?: string
  status?: string
  max_players?: number
  scenario?: { name?: string } | null
  players?: unknown[] | null
}
