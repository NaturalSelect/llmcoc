// @vitest-environment node
import { describe, expect, test } from 'vitest'
import type { LobbyPlayer } from '@/api/sessionTypes'
import type { SessionPlayer } from '@/api/types'
import {
  buildCompanionPayload,
  canAddCompanion,
  canRemoveCompanion,
  companionCardMeta,
  companionPoolState,
  findCompanion,
  humanPlayerCount,
  isCompanionPlayer,
  poolCandidates,
  startBlockedReason,
} from './companion'

const human = (id: number): SessionPlayer => ({ user_id: id, user: { username: 'u' + id, role: 'user' } })
const bot = (id: number): SessionPlayer => ({ user_id: id, user: { username: 'AI陪玩', role: 'bot' } })
const lobby = (players: SessionPlayer[] | null, over: Record<string, unknown> = {}) => ({
  status: 'lobby',
  max_players: 4,
  players,
  ...over,
})

describe('isCompanionPlayer', () => {
  test('只认 user.role === bot，缺字段、管理员、空值都不是 AI', () => {
    expect(isCompanionPlayer(bot(9))).toBe(true)
    expect(isCompanionPlayer(human(1))).toBe(false)
    // NOTE: 经 SessionPlayer 类型过一遍：入参类型只要求 user.role，直接写字面量会触发多余属性检查
    const sp = (p: SessionPlayer): SessionPlayer => p
    expect(isCompanionPlayer(sp({ user_id: 2, user: { username: 'a', role: 'admin' } }))).toBe(false)
    expect(isCompanionPlayer(sp({ user_id: 3, user: { username: 'a' } }))).toBe(false)
    expect(isCompanionPlayer(sp({ user_id: 4, user: null }))).toBe(false)
    expect(isCompanionPlayer(sp({ user_id: 5 }))).toBe(false)
    expect(isCompanionPlayer(null)).toBe(false)
    expect(isCompanionPlayer(undefined)).toBe(false)
  })

  test('大厅列表里的玩家（LobbyPlayer）同样识别', () => {
    const p: LobbyPlayer = { user_id: 9, user: { id: 9, username: 'AI陪玩', role: 'bot' } }
    expect(isCompanionPlayer(p)).toBe(true)
    expect(isCompanionPlayer({ user_id: 1, user: { id: 1, username: 'a' } } as LobbyPlayer)).toBe(false)
  })
})

describe('findCompanion / humanPlayerCount', () => {
  test('找到 AI 席位并统计真人数；players 为 null 或 undefined 也不抛错', () => {
    const players = [human(1), bot(9), human(2)]
    expect(findCompanion(players)?.user_id).toBe(9)
    expect(findCompanion([human(1)])).toBeUndefined()
    expect(findCompanion(null)).toBeUndefined()
    expect(humanPlayerCount(players)).toBe(2)
    expect(humanPlayerCount([bot(9)])).toBe(0)
    expect(humanPlayerCount(undefined)).toBe(0)
  })
})

describe('startBlockedReason', () => {
  test('没人、只有 AI 时给出原因，有真人时为空串', () => {
    expect(startBlockedReason([])).toBe('还没有玩家加入，无法开始')
    expect(startBlockedReason(null)).toBe('还没有玩家加入，无法开始')
    expect(startBlockedReason([bot(9)])).toContain('只有 AI')
    expect(startBlockedReason([human(1)])).toBe('')
    expect(startBlockedReason([bot(9), human(1)])).toBe('')
  })
})

describe('canAddCompanion / canRemoveCompanion', () => {
  test('添加：必须是大厅、有管理权限、还没有 AI 且未满员', () => {
    expect(canAddCompanion(lobby([human(1)]), true)).toBe(true)
    expect(canAddCompanion(lobby(null), true)).toBe(true)
    expect(canAddCompanion(lobby([human(1)]), false)).toBe(false)
    expect(canAddCompanion(lobby([human(1)], { status: 'playing' }), true)).toBe(false)
    expect(canAddCompanion(lobby([human(1)], { status: 'ended' }), true)).toBe(false)
    expect(canAddCompanion(lobby([human(1), bot(9)]), true)).toBe(false)
    expect(canAddCompanion(lobby([human(1), human(2)], { max_players: 2 }), true)).toBe(false)
    expect(canAddCompanion(lobby([human(1)], { max_players: 2 }), true)).toBe(true)
    expect(canAddCompanion(null, true)).toBe(false)
  })

  test('max_players 缺失时不在前端拦截', () => {
    expect(canAddCompanion(lobby([human(1), human(2)], { max_players: undefined }), true)).toBe(true)
    expect(canAddCompanion(lobby([human(1)], { max_players: 0 }), true)).toBe(true)
  })

  test('移除：大厅阶段、有管理权限且房间里确实有 AI', () => {
    expect(canRemoveCompanion(lobby([human(1), bot(9)]), true)).toBe(true)
    expect(canRemoveCompanion(lobby([human(1)]), true)).toBe(false)
    expect(canRemoveCompanion(lobby([human(1), bot(9)]), false)).toBe(false)
    expect(canRemoveCompanion(lobby([human(1), bot(9)], { status: 'playing' }), true)).toBe(false)
    expect(canRemoveCompanion(null, true)).toBe(false)
  })
})

describe('buildCompanionPayload', () => {
  test('没选人物卡时不提交', () => {
    expect(buildCompanionPayload({ character_card_id: '' })).toBeNull()
  })

  test('HO 编号仅非空时才附带，并去掉首尾空白', () => {
    expect(buildCompanionPayload({ character_card_id: 5 })).toStrictEqual({ character_card_id: 5 })
    expect(buildCompanionPayload({ character_card_id: 5, handout_code: '  ' })).toStrictEqual({ character_card_id: 5 })
    expect(buildCompanionPayload({ character_card_id: 5, handout_code: ' HO1 ' })).toStrictEqual({ character_card_id: 5, handout_code: 'HO1' })
    expect(buildCompanionPayload({ character_card_id: 5, handout_code: 'random' })).toStrictEqual({ character_card_id: 5, handout_code: 'random' })
  })
})

describe('卡池展示', () => {
  test('companionCardMeta：职业必有，年龄和性别缺省时不拼空字段', () => {
    expect(companionCardMeta({ occupation: '记者', age: 28, gender: '女' })).toBe('记者 · 28 岁 · 女')
    expect(companionCardMeta({ occupation: '', age: 0, gender: '' })).toBe('未知职业')
    expect(companionCardMeta({ occupation: '医生', gender: '男' })).toBe('医生 · 男')
  })

  test('companionPoolState：失活或死亡优先于对局中，其次才是空闲', () => {
    expect(companionPoolState({ is_active: true, wound_state: 'none', in_session: false })).toBe('idle')
    expect(companionPoolState({ is_active: true, wound_state: '', in_session: true })).toBe('in_session')
    expect(companionPoolState({ is_active: false, wound_state: 'none', in_session: false })).toBe('inactive')
    expect(companionPoolState({ is_active: true, wound_state: 'dead', in_session: false })).toBe('inactive')
    expect(companionPoolState({ is_active: false, wound_state: 'dead', in_session: true })).toBe('inactive')
  })

  test('poolCandidates：排除失活、死亡、对局中和没有 id 的卡，缺字段当作可用', () => {
    const cards = [
      { id: 1, name: '正常' },
      { id: 2, is_active: false },
      { id: 3, wound_state: 'dead' },
      { id: 4, in_session: true },
      { name: '没有id' },
      { id: 6, is_active: true, wound_state: 'none', in_session: false },
    ]
    expect(poolCandidates(cards).map((c) => c.id)).toStrictEqual([1, 6])
    expect(poolCandidates([])).toStrictEqual([])
  })
})
