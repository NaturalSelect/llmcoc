// NOTE: 只列出游戏页目前用到的字段；其余页面迁移时再按后端 handler 的 JSON 补全

export interface CardStats {
  hp?: number
  san?: number
  [key: string]: unknown
}

export interface CharacterCard {
  id?: number
  name?: string
  stats?: CardStats
  [key: string]: unknown
}

export interface SessionPlayer {
  user_id: number
  user?: { username?: string } | null
  character_card?: CharacterCard | null
  [key: string]: unknown
}

export interface Session {
  id: number
  status: string
  created_by: number
  players?: SessionPlayer[]
  [key: string]: unknown
}
