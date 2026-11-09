// @vitest-environment node
import { describe, expect, test } from 'vitest'
import type { Session } from '@/api/types'
import { diffHud, endSessionMessage, hudChips, isInSession, myCard, ownCardAccess, playerCardAccess, statNum, statusLabel } from './hud'

const session = (over: Partial<Session> = {}): Session => ({
  id: 7,
  status: 'playing',
  created_by: 1,
  players: [
    { user_id: 1, character_card: { id: 10, name: '艾米', stats: { hp: 10, san: 50 } } },
    { user_id: 2, character_card: null },
  ],
  ...over,
})

describe('hud', () => {
  test('myCard / isInSession / statNum', () => {
    expect(myCard(session(), 1)?.name).toBe('艾米')
    expect(myCard(session(), 2)).toBeNull()
    expect(myCard(session(), undefined)).toBeNull()
    expect(myCard(null, 1)).toBeNull()
    expect(isInSession(session(), 2)).toBe(true)
    expect(isInSession(session(), 3)).toBe(false)
    expect(statNum({ stats: { hp: '12' as unknown as number } }, 'hp')).toBe(12)
    expect(statNum({ stats: {} }, 'hp')).toBe(0)
    expect(statNum(null, 'hp')).toBe(0)
  })

  test('diffHud：首次与换房不闪，HP/SAN 下降才闪，回升不闪', () => {
    const card = (hp: number, san: number) => ({ stats: { hp, san } })
    const first = diffHud(null, 7, card(10, 50))
    expect(first.flash).toStrictEqual({ hp: false, san: false })
    expect(first.next).toStrictEqual({ sid: 7, hp: 10, san: 50 })

    expect(diffHud(first.next, 8, card(1, 1)).flash).toStrictEqual({ hp: false, san: false })
    expect(diffHud(first.next, 7, card(8, 50)).flash).toStrictEqual({ hp: true, san: false })
    expect(diffHud(first.next, 7, card(10, 40)).flash).toStrictEqual({ hp: false, san: true })
    expect(diffHud(first.next, 7, card(12, 60)).flash).toStrictEqual({ hp: false, san: false })
    expect(diffHud(first.next, 7, null).next).toBeNull()
    expect(diffHud(first.next, null, card(1, 1)).next).toBeNull()
  })

  test('hudChips：伤势、昏迷、疯狂', () => {
    expect(hudChips(null)).toStrictEqual([])
    expect(hudChips({ wound_state: 'none' })).toStrictEqual([])
    expect(hudChips({ wound_state: 'dead' })).toStrictEqual([{ text: '死亡' }])
    expect(hudChips({ wound_state: 'dying' })).toStrictEqual([{ text: '濒死' }])
    expect(hudChips({ wound_state: 'major' })).toStrictEqual([{ text: '重伤' }])
    expect(hudChips({ is_unconscious: true, madness_state: 'temporary', madness_symptom: '尖叫' })).toStrictEqual([
      { text: '昏迷' },
      { text: '疯狂', title: '尖叫' },
    ])
    expect(hudChips({ madness_state: 'none' })).toStrictEqual([])
  })

  test('查看人物卡的权限', () => {
    const s = session()
    const [mine, empty] = s.players!
    expect(playerCardAccess(mine, { id: 1, role: 'user' })).toMatchObject({ ok: true })
    expect(playerCardAccess(empty, { id: 2, role: 'user' })).toStrictEqual({ ok: false, error: '该玩家未绑定人物卡' })
    expect(playerCardAccess(mine, { id: 3, role: 'user' })).toStrictEqual({ ok: false, error: '只能查看自己的角色详细信息' })
    expect(playerCardAccess(mine, { id: 3, role: 'admin' })).toMatchObject({ ok: true })

    expect(ownCardAccess(s, { id: 1, role: 'user' })).toMatchObject({ ok: true })
    expect(ownCardAccess(s, { id: 2, role: 'user' })).toStrictEqual({ ok: false, error: '你没有以调查员身份参与本房间' })
    expect(ownCardAccess(null, null)).toStrictEqual({ ok: false, error: '你没有以调查员身份参与本房间' })
    expect(ownCardAccess(session({ status: 'lobby' }), { id: 3, role: 'user' })).toStrictEqual({
      ok: false,
      error: '你还没有携带人物卡加入本房间，请回到大厅点「加入」选择人物卡',
    })
  })

  test('结束游戏确认文案与状态标签', () => {
    expect(endSessionMessage(1, 200)).toBe('确认结束游戏？将消耗200金币，系统将进行成长评估。')
    expect(endSessionMessage(3, 200)).toBe('确认结束游戏？3名玩家每人将消耗200金币（共600金币），系统将进行成长评估。')
    expect(statusLabel('lobby')).toBe('等待中')
    expect(statusLabel('playing')).toBe('游戏中')
    expect(statusLabel('ended')).toBe('已结束')
    expect(statusLabel(undefined)).toBe('已结束')
  })
})
