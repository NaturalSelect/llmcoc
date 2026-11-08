import { describe, expect, test } from 'vitest'
import { describeEvent, eventsOf, lastEventTime } from './events'
import type { GameEvent } from './events'

function view(ev: GameEvent) {
  const v = describeEvent(ev)
  return [v.text, v.value, v.note].filter(Boolean).join(' ')
}

describe('eventsOf', () => {
  test('丢掉不合法的条目和错误类型的字段', () => {
    const msg = {
      id: 1,
      kind: 'event',
      meta: { events: [{ type: 'dice', roll: 45, skill: 7, hidden: 'yes' }, null, { roll: 3 }, 'x', { type: 'time', time: '第1天 09:00' }] },
    }
    expect(eventsOf(msg)).toStrictEqual([{ type: 'dice', roll: 45 }, { type: 'time', time: '第1天 09:00' }])
    expect(eventsOf({ id: 2, meta: { events: 'nope' } })).toStrictEqual([])
    expect(eventsOf(null)).toStrictEqual([])
  })

  test('lastEventTime 取最后一次时间推进', () => {
    const msg = { id: 1, kind: 'event', meta: { events: [{ type: 'time', time: 'A' }, { type: 'dice' }, { type: 'time', time: 'B' }] } }
    expect(lastEventTime(msg)).toBe('B')
    expect(lastEventTime({ id: 2, meta: { events: [{ type: 'dice' }] } })).toBe('')
  })
})

describe('describeEvent', () => {
  test('骰子：暗骰固定文案，公开骰只显示技能、点数、难度', () => {
    expect(view({ type: 'dice', hidden: true })).toBe('KP 暗骰了一次')
    expect(describeEvent({ type: 'dice', hidden: true }).icon).toBe('hidden')
    expect(view({ type: 'dice', character: '甲', skill: '侦查', roll: 45, level: '困难' })).toBe('甲 侦查 45 难度 困难')
    expect(view({ type: 'dice', skill: '潜行', roll: 12 })).toBe('潜行 12')
  })

  test('属性与技能变化', () => {
    expect(view({ type: 'stat', character: '甲', field: 'hp', from: 10, to: 7 })).toBe('甲 HP 10 → 7')
    expect(describeEvent({ type: 'stat', field: 'hp', from: 10, to: 7 }).tone).toBe('hp')
    expect(describeEvent({ type: 'stat', field: 'san', from: 50, to: 47 }).tone).toBe('san')
    expect(view({ type: 'stat', character: '甲', field: 'max_san', from: 99, to: 94 })).toBe('甲 SAN上限 99 → 94')
    expect(view({ type: 'stat', character: '甲', field: 'armor', from: 0, to: 2 })).toBe('甲 护甲 0 → 2')
    expect(view({ type: 'stat', character: '甲', field: 'skill', skill: '侦查', from: 40, to: 45 })).toBe('甲 侦查 40 → 45')
  })

  test('伤势', () => {
    expect(view({ type: 'wound', character: '甲', state: 'major' })).toBe('甲 受到重伤')
    expect(describeEvent({ type: 'wound', state: 'dead' }).tone).toBe('danger')
    expect(view({ type: 'wound', character: '甲', state: 'conscious' })).toBe('甲 恢复意识')
    expect(describeEvent({ type: 'wound', state: 'none' }).tone).toBe('plain')
  })

  test('物品、法术、神话存在、关系、资产', () => {
    expect(view({ type: 'item', character: '甲', category: 'inventory', op: 'add', name: '手电筒' })).toBe('甲 获得：手电筒')
    expect(view({ type: 'item', character: '甲', category: 'inventory', op: 'change', name: '子弹', detail: '子弹(x6) → 子弹(x5)' })).toBe(
      '甲 物品变化：子弹 子弹(x6) → 子弹(x5)',
    )
    expect(view({ type: 'item', character: '甲', category: 'spell', op: 'remove', name: '联络食尸鬼' })).toBe('甲 遗失法术：联络食尸鬼')
    expect(view({ type: 'item', character: '甲', category: 'monster', op: 'add', name: '深潜者' })).toBe('甲 记录神话存在：深潜者')
    expect(view({ type: 'item', character: '甲', category: 'relation', op: 'add', name: '老约翰(线人)' })).toBe('甲 记录关系：老约翰(线人)')
    expect(describeEvent({ type: 'item', category: 'asset', op: 'add', name: '旧宅' }).icon).toBe('asset')
  })

  test('疯狂', () => {
    expect(view({ type: 'madness', character: '甲', state: 'temporary', symptom: '失忆', duration: 5 })).toBe('甲 临时性疯狂 失忆（持续 5）')
    expect(view({ type: 'madness', character: '甲', state: 'none' })).toBe('甲 疯狂解除')
  })

  test('场景：位置、时间、战斗与追逐', () => {
    expect(view({ type: 'location', character: '甲', location: '地下室' })).toBe('甲 前往 地下室')
    expect(view({ type: 'time', time: '第2天 08:30(距开局已过24h30m)' })).toBe('时间来到 第2天 08:30')
    expect(view({ type: 'combat', op: 'start' })).toBe('战斗开始')
    expect(view({ type: 'combat', op: 'round', round: 2 })).toBe('战斗进入第 2 轮')
    expect(view({ type: 'chase', op: 'end' })).toBe('追逐结束')
  })
})
