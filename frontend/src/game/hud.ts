// NOTE: 游戏页角色状态条与房间操作的纯逻辑，不依赖 Vue 与网络
import type { CharacterCard, Session, SessionPlayer } from '@/api/types'

export interface SessionUser {
  id: number
  role: string
}

export function myCard(session: Session | null, userId: number | undefined): CharacterCard | null {
  if (!session || !userId) return null
  return session.players?.find((p) => p.user_id === userId)?.character_card || null
}

export function isInSession(session: Session | null, userId: number | undefined): boolean {
  return !!session?.players?.some((p) => p.user_id === userId)
}

export function statNum(card: CharacterCard | null | undefined, key: string): number {
  return Number(card?.stats?.[key]) || 0
}

export interface HudSnapshot {
  sid: number
  hp: number
  san: number
}

export interface HudFlash {
  hp: boolean
  san: boolean
}

export const NO_FLASH: HudFlash = { hp: false, san: false }

// NOTE: HP/SAN 下降时闪一次高亮；比较基准带房间 id，换房或没有人物卡时重置，避免换房误闪
export function diffHud(
  prev: HudSnapshot | null,
  sid: number | null | undefined,
  card: CharacterCard | null,
): { next: HudSnapshot | null; flash: HudFlash } {
  if (!card || !sid) return { next: null, flash: NO_FLASH }
  const next = { sid, hp: statNum(card, 'hp'), san: statNum(card, 'san') }
  if (!prev || prev.sid !== sid) return { next, flash: NO_FLASH }
  return { next, flash: { hp: next.hp < prev.hp, san: next.san < prev.san } }
}

export interface HudChip {
  text: string
  title?: string
}

export function hudChips(card: CharacterCard | null | undefined): HudChip[] {
  if (!card) return []
  const c = card as Record<string, unknown>
  const chips: HudChip[] = []
  const wound = String(c.wound_state ?? '')
  if (wound && wound !== 'none') chips.push({ text: wound === 'dead' ? '死亡' : wound === 'dying' ? '濒死' : '重伤' })
  if (c.is_unconscious) chips.push({ text: '昏迷' })
  const madness = String(c.madness_state ?? '')
  if (madness && madness !== 'none') chips.push({ text: '疯狂', title: String(c.madness_symptom ?? '') })
  return chips
}

export type CardAccess = { ok: true; card: CharacterCard } | { ok: false; error: string }

// NOTE: 调查员列表里点别人的卡只有管理员能看，普通玩家只能看自己的
export function playerCardAccess(player: SessionPlayer, user: SessionUser | null): CardAccess {
  if (!player.character_card) return { ok: false, error: '该玩家未绑定人物卡' }
  if (player.user_id !== user?.id && user?.role !== 'admin') return { ok: false, error: '只能查看自己的角色详细信息' }
  return { ok: true, card: player.character_card }
}

export function ownCardAccess(session: Session | null, user: SessionUser | null): CardAccess {
  const card = myCard(session, user?.id)
  if (card) return { ok: true, card }
  // NOTE: 房主创建房间后不会自动加入，只有等待中的房间还能补上人物卡
  const hint = session?.status === 'lobby' ? '你还没有携带人物卡加入本房间，请回到大厅点「加入」选择人物卡' : '你没有以调查员身份参与本房间'
  return { ok: false, error: hint }
}

export function endSessionMessage(playerCount: number, costPerPlayer: number): string {
  return playerCount > 1
    ? `确认结束游戏？${playerCount}名玩家每人将消耗${costPerPlayer}金币（共${costPerPlayer * playerCount}金币），系统将进行成长评估。`
    : `确认结束游戏？将消耗${costPerPlayer}金币，系统将进行成长评估。`
}

export const STATUS_LABEL: Record<string, string> = { lobby: '等待中', playing: '游戏中' }

export function statusLabel(status: string | undefined): string {
  return (status && STATUS_LABEL[status]) || '已结束'
}
