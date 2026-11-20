// NOTE: AI 陪玩的纯逻辑辅助，不依赖 Vue 与网络。AI 占房间里一个玩家名额，身份只靠 user.role === 'bot' 识别；
// 每个房间至多一个 AI，增删只在大厅阶段、由房主或管理员操作（后端同样校验，这里只决定按钮显不显示）
import type { AddCompanionForm, AddCompanionPayload } from '@/api/companionTypes'

export interface CompanionPlayerLike {
  user?: { role?: string } | null
}

export interface CompanionSessionLike {
  status: string
  max_players?: unknown
  players?: ReadonlyArray<CompanionPlayerLike> | null
}

export function isCompanionPlayer(p: CompanionPlayerLike | null | undefined): boolean {
  return p?.user?.role === 'bot'
}

export function findCompanion<T extends CompanionPlayerLike>(players: ReadonlyArray<T> | null | undefined): T | undefined {
  return (players ?? []).find((p) => isCompanionPlayer(p))
}

export function humanPlayerCount(players: ReadonlyArray<CompanionPlayerLike> | null | undefined): number {
  return (players ?? []).filter((p) => !isCompanionPlayer(p)).length
}

// NOTE: 开始游戏至少要有一名真人，只有 AI 的房间后端会返回 400；返回空串表示可以开始，否则是按钮上要给玩家看的原因
export function startBlockedReason(players: ReadonlyArray<CompanionPlayerLike> | null | undefined): string {
  if (!players?.length) return '还没有玩家加入，无法开始'
  if (humanPlayerCount(players) === 0) return '房间里只有 AI 陪玩，至少需要一名真人玩家才能开始'
  return ''
}

// NOTE: max_players 缺失或非正数时不在前端拦截，交给后端判断
export function canAddCompanion(session: CompanionSessionLike | null | undefined, canManage: boolean): boolean {
  if (!session || !canManage || session.status !== 'lobby') return false
  const players = session.players ?? []
  if (findCompanion(players)) return false
  const max = Number(session.max_players)
  return !(max > 0 && players.length >= max)
}

export function canRemoveCompanion(session: CompanionSessionLike | null | undefined, canManage: boolean): boolean {
  if (!session || !canManage || session.status !== 'lobby') return false
  return findCompanion(session.players) !== undefined
}

// NOTE: 没选人物卡时不提交（按钮禁用兜底）；HO 编号仅非空时才附带，与加入房间的请求体保持一致
export function buildCompanionPayload(form: AddCompanionForm): AddCompanionPayload | null {
  if (!form.character_card_id) return null
  const payload: AddCompanionPayload = { character_card_id: form.character_card_id }
  const handoutCode = (form.handout_code ?? '').trim()
  if (handoutCode) payload.handout_code = handoutCode
  return payload
}

// NOTE: 卡池里的人物卡列表显示用，年龄和性别缺省时不拼空字段
export function companionCardMeta(c: { occupation?: string; age?: number; gender?: string }): string {
  const parts = [c.occupation || '未知职业']
  if (c.age) parts.push(`${c.age} 岁`)
  if (c.gender) parts.push(c.gender)
  return parts.join(' · ')
}

export type CompanionPoolState = 'idle' | 'in_session' | 'inactive'

// NOTE: 管理员卡池页的状态标签：死亡或失活的卡优先显示（不会再被房间选到），其次才是对局中和空闲
export function companionPoolState(c: { is_active: boolean; wound_state?: string; in_session: boolean }): CompanionPoolState {
  if (!c.is_active || c.wound_state === 'dead') return 'inactive'
  return c.in_session ? 'in_session' : 'idle'
}

export const COMPANION_POOL_LABEL: Record<CompanionPoolState, string> = {
  idle: '空闲',
  in_session: '对局中',
  inactive: '已死亡或失活',
}

// NOTE: 管理员「从我的卡移入」的候选：只要自己名下、活跃、没死亡、没被房间占用的卡；
// /api/characters 的卡类型是松散的，这里用 unknown 字段收窄，缺字段（旧响应）当作可用
export function poolCandidates<T extends { id?: number; is_active?: unknown; wound_state?: unknown; in_session?: unknown }>(
  cards: ReadonlyArray<T>,
): T[] {
  return cards.filter((c) => typeof c.id === 'number' && c.is_active !== false && c.wound_state !== 'dead' && c.in_session !== true)
}
