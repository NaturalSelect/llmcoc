// NOTE: 调查员手册与实时战斗/追逐追踪器的前端数据类型及安全归一化工具

export interface SessionClue {
  id: number
  session_id: number
  title: string
  content: string
  source?: string
  added_by: string
  user_id?: number | null
  created_at?: string
}

export interface SessionNote {
  id: number
  session_id: number
  user_id: number
  author_name: string
  content: string
  is_shared: boolean
  created_at?: string
}

export interface HandbookLocation {
  name: string
  occupants: string[]
}

export interface HandbookNpc {
  name: string
  occupation?: string
  location?: string
  is_alive: boolean
}

export interface SessionClock {
  name: string
  current: number
  max: number
  stage?: string
  description?: string
  visible: boolean
}

export interface HandbookCombatParticipant {
  name: string
  dex: number
  is_npc: boolean
  has_acted: boolean
  has_dodged_or_fb: boolean
  is_aiming: boolean
  pending_clarification: boolean
  pending_question?: string
  is_alive: boolean
}

export interface HandbookCombat {
  active: boolean
  round: number
  actor_index: number
  participants: HandbookCombatParticipant[]
}

export interface HandbookChaseParticipant {
  name: string
  dex: number
  mov: number
  location: number
  is_npc: boolean
  is_pursuer: boolean
  has_acted: boolean
  pending_clarification: boolean
  pending_question?: string
}

export interface HandbookChaseObstacle {
  name: string
  between: [number, number]
  hp: number
  max_hp: number
}

export interface HandbookChase {
  active: boolean
  round: number
  min_mov: number
  actor_index: number
  participants: HandbookChaseParticipant[]
  obstacles: HandbookChaseObstacle[]
}

export interface HandbookData {
  clues: SessionClue[]
  notes: SessionNote[]
  locations: HandbookLocation[]
  npcs: HandbookNpc[]
  clocks: SessionClock[]
  combat: HandbookCombat | null
  chase: HandbookChase | null
}

export function emptyHandbook(): HandbookData {
  return {
    clues: [],
    notes: [],
    locations: [],
    npcs: [],
    clocks: [],
    combat: null,
    chase: null,
  }
}

export function normalizeHandbook(raw: unknown): HandbookData {
  if (!raw || typeof raw !== 'object') return emptyHandbook()
  const r = raw as Partial<HandbookData>
  return {
    clues: Array.isArray(r.clues) ? r.clues : [],
    notes: Array.isArray(r.notes) ? r.notes : [],
    locations: Array.isArray(r.locations) ? r.locations : [],
    npcs: Array.isArray(r.npcs) ? r.npcs : [],
    clocks: Array.isArray(r.clocks) ? r.clocks : [],
    combat: r.combat && typeof r.combat === 'object' && r.combat.active ? r.combat : null,
    chase: r.chase && typeof r.chase === 'object' && r.chase.active ? r.chase : null,
  }
}
