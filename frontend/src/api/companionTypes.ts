// NOTE: AI 陪玩接口的请求与响应结构，按后端契约整理（internal/handlers/admin_companion.go 的 companionCardView 等）。
// 房间选卡和管理员卡池是同一种卡，只是管理员页多了占用与失活状态

// GET /api/sessions/:id/companion-cards 返回的一张空闲卡
export interface CompanionCard {
  id: number
  name: string
  occupation: string
  age: number
  gender: string
  portrait_hash: string
  hp: number
  max_hp: number
  san: number
  max_san: number
}

export interface CompanionCardsResponse {
  // false 表示管理员没有启用 companion 角色，房间里不能添加 AI
  enabled: boolean
  cards: CompanionCard[]
}

// POST /api/sessions/:id/companion；handout_code 可以是预设 HO 编号或 'random'，含义与加入房间一致。
// NOTE: 契约里还有可选的 secret_background，但加入房间的前端已经不再提供秘密背景输入，这里同样不发送
export interface AddCompanionPayload {
  character_card_id: number
  handout_code?: string
}

export interface AddCompanionForm {
  character_card_id: number | ''
  handout_code?: string
}

// 成功响应与加入房间接口同形：随机或指定了 HO 时带回实际分到的编号
export interface AddCompanionResponse {
  handout_code?: string
  message?: string
}

// GET /api/admin/companion-cards 返回的卡池条目
export interface AdminCompanionCard extends CompanionCard {
  is_active: boolean
  wound_state: string
  // 正被某个房间占用
  in_session: boolean
}

export interface CompanionMessageResponse {
  message?: string
}
