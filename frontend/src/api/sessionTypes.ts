import type { Page } from './pagination'
import type { Scenario } from './scenarioTypes'
import type { CharacterCard } from './types'

// NOTE: 跑团大厅用的房间结构，按 GET /api/sessions 等接口的 JSON 补全。
// 不继承 types.ts 的 Session：服务端 Go 的 nil 切片会序列化成 null（例如刚创建的房间 players 为 null），基类的 players 不允许 null

export type SessionStatus = 'lobby' | 'playing' | 'ended'

export interface SessionUserRef {
  id?: number
  username?: string
  [key: string]: unknown
}

export interface LobbyPlayer {
  id?: number
  user_id: number
  character_card_id?: number
  user?: SessionUserRef | null
  character_card?: CharacterCard | null
  [key: string]: unknown
}

export interface LobbySession {
  id: number
  name: string
  status: string
  scenario_id?: number
  max_players: number
  has_password?: boolean
  enable_nsfw?: boolean
  evolve_backstory?: boolean
  created_by: number
  created_at?: string
  updated_at?: string
  scenario?: Scenario | null
  creator?: SessionUserRef | null
  players?: LobbyPlayer[] | null
  [key: string]: unknown
}

// 分页信封比 pagination.ts 的 Page 多一个 page_size
export interface PagedResponse<T> extends Page<T> {
  page_size?: number
}

export interface CreateSessionForm {
  name: string
  scenario_id: number | ''
  max_players: number | ''
  password: string
  enable_nsfw: boolean
  evolve_backstory: boolean
}

export interface CreateSessionPayload {
  name: string
  scenario_id: number
  max_players: number
  password: string
  enable_nsfw: boolean
  evolve_backstory: boolean
}

export interface JoinSessionForm {
  character_card_id: number | ''
  password: string
}

// start/end/leave/revive 等接口返回的 { message }
export interface MessageResponse {
  message?: string
}

// ── 模组 content 的细分结构（Scenario.content 在 scenarioTypes.ts 里是宽松 Record，这里给渲染用的窄类型）──

export interface ScenarioScene {
  id?: string
  name?: string
  description?: string
  triggers?: string[] | null
}

export interface ScenarioNpc {
  name?: string
  race?: string
  occupation?: string
  description?: string
  attitude?: string
  stats?: Record<string, number> | null
  skills?: Record<string, number> | null
  spells?: string[] | null
  cthulhu_mythos_skill?: number | null
}

export interface ScenarioClue {
  summary?: string
  source?: string
  skill_check?: string
  on_success?: string
  on_failure?: string
  nature?: string
}

export interface ScenarioEnding {
  name?: string
  trigger?: string
  description?: string
  san_reward?: string
  is_failure?: boolean
}

export interface ScenarioTimelineEvent {
  time?: string
  event?: string
  phase?: string
}

export interface ScenarioRewardInfo {
  name?: string
  type?: string
  description?: string
  mechanics_note?: string
  find_condition?: string
}

export interface ScenarioKeeperAppendix {
  core_truth?: string
  antagonist_dossier?: string
  difficulty_down?: string
  difficulty_up?: string
  solo_advice?: string
  group_advice?: string
  horror_tips?: string
  theme_guidance?: string
}

export interface ScenarioMechanicStage {
  label?: string
  effect?: string
  trigger?: string
}

export interface ScenarioMechanic {
  name?: string
  type?: string
  description?: string
  stages?: ScenarioMechanicStage[] | null
}

export interface ScenarioContentView {
  setting?: string
  intro?: string
  game_start_slot?: number | string | null
  map_description?: string
  playthrough_outline?: string
  scenes?: ScenarioScene[] | null
  npcs?: ScenarioNpc[] | null
  clues?: ScenarioClue[] | null
  endings?: ScenarioEnding[] | null
  reward?: ScenarioRewardInfo | null
  timeline?: ScenarioTimelineEvent[] | null
  keeper_appendix?: ScenarioKeeperAppendix | null
  mechanics?: ScenarioMechanic[] | null
  mythos_core?: string
}
