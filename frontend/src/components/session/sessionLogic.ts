import type { Scenario } from '@/api/scenarioTypes'
import type { CreateSessionForm, CreateSessionPayload, JoinSessionForm, LobbyPlayer, LobbySession } from '@/api/sessionTypes'
import type { CharacterCard } from '@/api/types'

export type StatusTone = 'ledger' | 'seal' | 'faint'

export function statusLabel(status: string | undefined): string {
  return status === 'lobby' ? '等待中' : status === 'playing' ? '游戏中' : '已结束'
}

export function statusTone(status: string | undefined): StatusTone {
  return status === 'lobby' ? 'ledger' : status === 'playing' ? 'seal' : 'faint'
}

// NOTE: 刚创建的房间 players 可能是 null，统一在这里兜底
export function sessionPlayers(s: Pick<LobbySession, 'players'>): LobbyPlayer[] {
  return s.players ?? []
}

export function isInSession(s: Pick<LobbySession, 'players'>, userId: number | undefined): boolean {
  return sessionPlayers(s).some((p) => p.user_id === userId)
}

export function playerCountLabel(s: Pick<LobbySession, 'players' | 'max_players'>): string {
  return `${sessionPlayers(s).length}/${s.max_players}`
}

// NOTE: 仅供加入房间使用的候选人物卡：活跃、未死亡、HP>0；不改全局人物卡列表，商城/复活列表不受影响
export function joinableCharacters(cards: CharacterCard[]): CharacterCard[] {
  return cards.filter((c) => c.is_active !== false && c.wound_state !== 'dead' && (c.stats?.hp ?? 0) > 0)
}

export function cardOptionLabel(c: CharacterCard): string {
  return `${c.name ?? ''} (${String(c.occupation || '未知职业')})`
}

export function scenarioOptionLabel(s: Scenario): string {
  return `${s.name} (${s.min_players ?? 1}-${s.max_players ?? 4}人)`
}

export function emptyCreateForm(): CreateSessionForm {
  return { name: '', scenario_id: '', max_players: 4, password: '', enable_nsfw: false, evolve_backstory: false }
}

export function emptyJoinForm(): JoinSessionForm {
  return { character_card_id: '', password: '' }
}

// NOTE: 没选人物卡时不提交（旧前端静默返回，这里由按钮禁用兜底）
export function buildJoinPayload(form: JoinSessionForm): { character_card_id: number; password: string } | null {
  if (!form.character_card_id) return null
  return { character_card_id: form.character_card_id, password: form.password }
}

// NOTE: 选剧本时若房间名还是空的，用剧本名做默认值；之后换剧本不会覆盖已有房间名
export function applyScenarioSelection(form: CreateSessionForm, s: Pick<Scenario, 'id' | 'name'>): CreateSessionForm {
  return { ...form, scenario_id: s.id, name: form.name || s.name }
}

// NOTE: 没选剧本或最终房间名为空时返回 null（旧前端此时静默不提交）。
// 剧本名取自 selected 缓存，翻页后选中项不在当前列表里也能用。
// max_players 被清空时传 0，由后端取剧本人数上限，避免把 "" 发给整型字段导致 400
export function buildCreatePayload(form: CreateSessionForm, selected: Pick<Scenario, 'name'> | null): CreateSessionPayload | null {
  if (!form.scenario_id) return null
  const name = form.name.trim() || selected?.name || ''
  if (!name) return null
  const max = typeof form.max_players === 'number' && Number.isFinite(form.max_players) ? form.max_players : 0
  return {
    name,
    scenario_id: form.scenario_id,
    max_players: max,
    password: form.password,
    enable_nsfw: form.enable_nsfw,
    evolve_backstory: form.evolve_backstory,
  }
}

export function endSessionConfirmMessage(playerCount: number, costPerPlayer: number): string {
  return playerCount > 1
    ? `确认结束游戏？${playerCount}名玩家每人将消耗${costPerPlayer}金币（共${costPerPlayer * playerCount}金币），系统将进行成长评估。`
    : `确认结束游戏？将消耗${costPerPlayer}金币，系统将进行成长评估。`
}

export function reviveConfirmMessage(s: { id: number; name?: string }): string {
  return `确认复活房间「${s.name || s.id}」？`
}

// NOTE: 取消收藏后当前页若被清空（只剩 1 条且不在第一页）回退一页
export function pageAfterRemoval(itemCount: number, page: number): number {
  return itemCount === 1 && page > 1 ? page - 1 : page
}

export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
