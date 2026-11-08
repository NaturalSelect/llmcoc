// @vitest-environment node
import { describe, expect, test } from 'vitest'
import type { CreateSessionForm, LobbySession } from '@/api/sessionTypes'
import {
  applyScenarioSelection,
  buildCreatePayload,
  buildJoinPayload,
  cardOptionLabel,
  emptyCreateForm,
  emptyJoinForm,
  endSessionConfirmMessage,
  errorMessage,
  isInSession,
  joinableCharacters,
  pageAfterRemoval,
  playerCountLabel,
  reviveConfirmMessage,
  scenarioOptionLabel,
  sessionPlayers,
  statusLabel,
  statusTone,
} from './sessionLogic'

const form = (over: Partial<CreateSessionForm> = {}): CreateSessionForm => ({ ...emptyCreateForm(), ...over })

describe('房间状态', () => {
  test('状态文案：lobby 等待中、playing 游戏中，其余一律已结束', () => {
    expect(statusLabel('lobby')).toBe('等待中')
    expect(statusLabel('playing')).toBe('游戏中')
    expect(statusLabel('ended')).toBe('已结束')
    expect(statusLabel(undefined)).toBe('已结束')
  })

  test('状态色调', () => {
    expect(statusTone('lobby')).toBe('ledger')
    expect(statusTone('playing')).toBe('seal')
    expect(statusTone('ended')).toBe('faint')
  })
})

describe('玩家列表', () => {
  const s = (players: LobbySession['players'], max = 4) => ({ players, max_players: max })

  test('players 为 null/undefined 时当作空列表', () => {
    expect(sessionPlayers(s(null))).toEqual([])
    expect(sessionPlayers(s(undefined))).toEqual([])
    expect(playerCountLabel(s(null))).toBe('0/4')
  })

  test('人数文案与是否在房间内', () => {
    const room = s([{ user_id: 1 }, { user_id: 2 }], 3)
    expect(playerCountLabel(room)).toBe('2/3')
    expect(isInSession(room, 2)).toBe(true)
    expect(isInSession(room, 9)).toBe(false)
    expect(isInSession(room, undefined)).toBe(false)
    expect(isInSession(s(null), 1)).toBe(false)
  })
})

describe('joinableCharacters', () => {
  test('排除停用、死亡、HP 为 0 或缺失的人物卡；is_active 缺省视为活跃', () => {
    const cards = [
      { id: 1, name: '正常', stats: { hp: 10 } },
      { id: 2, name: '停用', is_active: false, stats: { hp: 10 } },
      { id: 3, name: '死亡', wound_state: 'dead', stats: { hp: 10 } },
      { id: 4, name: '零血', stats: { hp: 0 } },
      { id: 5, name: '无属性' },
      { id: 6, name: '濒死', wound_state: 'dying', stats: { hp: 1 } },
    ]
    expect(joinableCharacters(cards).map((c) => c.id)).toEqual([1, 6])
  })

  test('不修改传入数组', () => {
    const cards = [{ id: 1, name: 'a', wound_state: 'dead', stats: { hp: 5 } }]
    joinableCharacters(cards)
    expect(cards).toHaveLength(1)
  })

  test('选项文案缺职业时显示未知职业', () => {
    expect(cardOptionLabel({ id: 1, name: '艾米', occupation: '记者' })).toBe('艾米 (记者)')
    expect(cardOptionLabel({ id: 2, name: '鲍勃' })).toBe('鲍勃 (未知职业)')
    expect(cardOptionLabel({ id: 3, name: '卡尔', occupation: '' })).toBe('卡尔 (未知职业)')
  })
})

describe('创建房间表单', () => {
  test('默认值与旧前端一致', () => {
    expect(emptyCreateForm()).toEqual({ name: '', scenario_id: '', max_players: 4, password: '', enable_nsfw: false })
    expect(emptyJoinForm()).toEqual({ character_card_id: '', password: '' })
  })

  test('剧本选项文案', () => {
    expect(scenarioOptionLabel({ id: 1, name: '雾中', min_players: 2, max_players: 4 })).toBe('雾中 (2-4人)')
  })

  test('选剧本：房间名为空才用剧本名，已有名字不覆盖', () => {
    const first = applyScenarioSelection(form(), { id: 3, name: '雾中' })
    expect(first).toMatchObject({ scenario_id: 3, name: '雾中' })
    const second = applyScenarioSelection({ ...first, name: '我的房间' }, { id: 4, name: '另一个' })
    expect(second).toMatchObject({ scenario_id: 4, name: '我的房间' })
  })

  test('applyScenarioSelection 返回新对象，不改原表单', () => {
    const f = form()
    applyScenarioSelection(f, { id: 3, name: '雾中' })
    expect(f.scenario_id).toBe('')
  })

  test('没选剧本时不提交', () => {
    expect(buildCreatePayload(form({ name: 'x' }), null)).toBeNull()
  })

  test('房间名为空时回落到选中剧本名；都没有则不提交', () => {
    expect(buildCreatePayload(form({ scenario_id: 3 }), { name: '雾中' })).toMatchObject({ name: '雾中', scenario_id: 3 })
    expect(buildCreatePayload(form({ scenario_id: 3, name: '   ' }), { name: '雾中' })?.name).toBe('雾中')
    expect(buildCreatePayload(form({ scenario_id: 3 }), null)).toBeNull()
    expect(buildCreatePayload(form({ scenario_id: 3 }), { name: '' })).toBeNull()
  })

  test('透传密码与 NSFW 开关；房间名去首尾空白', () => {
    const p = buildCreatePayload(form({ scenario_id: 3, name: ' 夜话 ', password: 'abc', enable_nsfw: true, max_players: 2 }), null)
    expect(p).toEqual({ name: '夜话', scenario_id: 3, max_players: 2, password: 'abc', enable_nsfw: true })
  })

  test('最大玩家数被清空时传 0，由后端取剧本上限，不会把空串发给整型字段', () => {
    expect(buildCreatePayload(form({ scenario_id: 3, name: 'x', max_players: '' }), null)?.max_players).toBe(0)
  })
})

describe('加入房间表单', () => {
  test('没选人物卡不提交', () => {
    expect(buildJoinPayload(emptyJoinForm())).toBeNull()
  })

  test('选了人物卡带上密码', () => {
    expect(buildJoinPayload({ character_card_id: 7, password: 'pw' })).toEqual({ character_card_id: 7, password: 'pw' })
  })
})

describe('确认文案', () => {
  test('结束游戏：多人显示总额，单人只显示每人额度', () => {
    expect(endSessionConfirmMessage(3, 200)).toBe('确认结束游戏？3名玩家每人将消耗200金币（共600金币），系统将进行成长评估。')
    expect(endSessionConfirmMessage(1, 200)).toBe('确认结束游戏？将消耗200金币，系统将进行成长评估。')
    expect(endSessionConfirmMessage(0, 50)).toBe('确认结束游戏？将消耗50金币，系统将进行成长评估。')
  })

  test('复活：优先房间名，没有名字用 id', () => {
    expect(reviveConfirmMessage({ id: 5, name: '雾中' })).toBe('确认复活房间「雾中」？')
    expect(reviveConfirmMessage({ id: 5, name: '' })).toBe('确认复活房间「5」？')
    expect(reviveConfirmMessage({ id: 5 })).toBe('确认复活房间「5」？')
  })
})

describe('其他', () => {
  test('取消收藏后：只剩一条且不在第一页才回退', () => {
    expect(pageAfterRemoval(1, 3)).toBe(2)
    expect(pageAfterRemoval(1, 1)).toBe(1)
    expect(pageAfterRemoval(5, 3)).toBe(3)
    expect(pageAfterRemoval(0, 2)).toBe(2)
  })

  test('errorMessage', () => {
    expect(errorMessage(new Error('炸了'))).toBe('炸了')
    expect(errorMessage('字符串')).toBe('字符串')
  })
})
