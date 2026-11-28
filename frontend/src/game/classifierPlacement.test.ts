// NOTE: 分类器记录摆进消息流的纯逻辑：输入分类按「周期 + 用户」挂玩家消息，路由挂周期最后一条玩家消息，floor 为 0 的输入分类按时间落在前一条消息之后
import { describe, expect, test } from 'vitest'
import type { ClassifierLog } from '@/api/classifierTypes'
import { emptyPlacement, placeClassifierLogs } from './classifierPlacement'
import type { RoomMessage } from './roomState'

function log(id: number, extra: Partial<ClassifierLog> = {}): ClassifierLog {
  return {
    id,
    kind: 'input_guard',
    created_at: '2026-11-28T10:00:00',
    user_id: 2,
    subject: '爱丽丝',
    floor: 0,
    decision: 'passed',
    status: 'ok',
    error: '',
    choice: 'in_character',
    score: 0.1,
    threshold: 0.8,
    probabilities: {},
    request: {},
    role: '',
    elapsed_ms: 1,
    request_id: '',
    ...extra,
  }
}

const route = (id: number, floor: number, extra: Partial<ClassifierLog> = {}) =>
  log(id, { kind: 'nsfw_route', user_id: 0, floor, decision: 'nsfw', role: 'director_nsfw', ...extra })

function player(id: number, floor: number, userId: number, createdAt?: string): RoomMessage {
  return { id, kind: 'player', role: 'user', status: 'done', floor, user_id: userId, content: `行动${id}`, created_at: createdAt }
}

function other(id: number, kind: string, createdAt?: string, floor = 1): RoomMessage {
  return { id, kind, role: 'assistant', status: 'done', floor, content: kind, created_at: createdAt }
}

const ids = (list: readonly ClassifierLog[] | undefined) => (list ?? []).map((l) => l.id)
const byMessage = (p: ReturnType<typeof placeClassifierLogs>) => Object.fromEntries([...p.byMessage].map(([k, v]) => [k, ids(v)]))
const after = (p: ReturnType<typeof placeClassifierLogs>) => Object.fromEntries([...p.after].map(([k, v]) => [k, ids(v)]))

describe('placeClassifierLogs：挂在玩家消息下面', () => {
  test('没有记录时返回空的摆放结果', () => {
    const p = placeClassifierLogs([player(4, 2, 2)], [])
    expect(p).toStrictEqual(emptyPlacement())
    expect(p.byMessage.size + p.leading.length + p.after.size).toBe(0)
  })

  test('输入分类按周期和用户匹配对应的玩家消息', () => {
    const msgs = [other(1, 'kp'), player(4, 2, 2), player(5, 2, 3), player(6, 3, 2)]
    const p = placeClassifierLogs(msgs, [log(1, { floor: 2, user_id: 2 }), log(2, { floor: 2, user_id: 3 }), log(3, { floor: 3, user_id: 2 })])
    expect(byMessage(p)).toStrictEqual({ 4: [1], 5: [2], 6: [3] })
    expect(p.leading).toStrictEqual([])
    expect(p.after.size).toBe(0)
  })

  test('同一玩家同一周期有多条玩家消息时，输入分类挂在最后一条', () => {
    const p = placeClassifierLogs([player(4, 2, 2), player(5, 2, 2)], [log(1, { floor: 2, user_id: 2 })])
    expect(byMessage(p)).toStrictEqual({ 5: [1] })
  })

  test('路由记录只挂在该周期最后一条玩家消息上，排在输入分类之后', () => {
    const msgs = [player(4, 2, 2), player(5, 2, 3), player(6, 3, 2)]
    const p = placeClassifierLogs(msgs, [route(1, 2), log(2, { floor: 2, user_id: 3 }), log(3, { floor: 2, user_id: 2 }), route(4, 3)])
    expect(byMessage(p)).toStrictEqual({ 4: [3], 5: [2, 1], 6: [4] })
  })

  test('同一周期的多条路由记录都挂在最后一条玩家消息上，保持传入顺序', () => {
    const p = placeClassifierLogs([player(4, 2, 2), player(5, 2, 3)], [route(1, 2), route(7, 2)])
    expect(byMessage(p)).toStrictEqual({ 5: [1, 7] })
  })

  test('没有对应玩家消息的记录不展示，也不会冒充孤儿行', () => {
    const msgs = [other(1, 'kp', '2026-11-28T10:00:00', 2), player(4, 2, 2)]
    const p = placeClassifierLogs(msgs, [log(1, { floor: 9, user_id: 2 }), log(2, { floor: 2, user_id: 99 }), route(3, 9), route(4, 0), route(5, -1)])
    expect(p.byMessage.size).toBe(0)
    expect(p.leading).toStrictEqual([])
    expect(p.after.size).toBe(0)
  })

  test('只有玩家消息参与匹配：同一周期的 KP 消息、floor 缺失或为 0 的玩家消息都不算', () => {
    const msgs = [other(1, 'kp', undefined, 2), other(2, 'ask', undefined, 2), player(3, 0, 2), { ...player(4, 0, 2), floor: undefined }]
    const p = placeClassifierLogs(msgs, [log(1, { floor: 2 }), route(2, 2)])
    expect(p.byMessage.size).toBe(0)
  })

  test('user_id 为字符串的玩家消息也能匹配', () => {
    const msg = { ...player(4, 2, 2), user_id: '2' }
    const p = placeClassifierLogs([msg], [log(1, { floor: 2, user_id: 2 })])
    expect(byMessage(p)).toStrictEqual({ 4: [1] })
  })

  test('同一个 id 的记录 floor 从 0 改成周期号后，从孤儿位置移到玩家消息下', () => {
    const msgs = [other(1, 'kp', '2026-11-28T10:00:00'), player(4, 2, 2, '2026-11-28T10:01:00')]
    const before = placeClassifierLogs(msgs, [log(1, { created_at: '2026-11-28T10:00:30' })])
    expect(after(before)).toStrictEqual({ 1: [1] })
    expect(before.byMessage.size).toBe(0)

    const moved = placeClassifierLogs(msgs, [log(1, { created_at: '2026-11-28T10:00:30', floor: 2 })])
    expect(byMessage(moved)).toStrictEqual({ 4: [1] })
    expect(moved.after.size).toBe(0)
    expect(moved.leading).toStrictEqual([])
  })
})

describe('placeClassifierLogs：孤儿行（输入分类 floor 为 0）', () => {
  const msgs = [
    other(1, 'kp', '2026-11-28T10:00:00'),
    other(2, 'ask', '2026-11-28T10:00:20'),
    other(3, 'event', '2026-11-28T10:00:30'),
    player(4, 2, 2, '2026-11-28T10:01:00'),
  ]
  const at = (id: number, time: string) => log(id, { created_at: time })

  test('放在时间上紧挨着的前一条消息之后，比所有消息都早的放最前面', () => {
    const p = placeClassifierLogs(msgs, [at(1, '2026-11-28T09:59:00'), at(2, '2026-11-28T10:00:10'), at(3, '2026-11-28T10:00:25'), at(4, '2026-11-28T10:00:45'), at(5, '2026-11-28T10:30:00')])
    expect(ids(p.leading)).toStrictEqual([1])
    expect(after(p)).toStrictEqual({ 1: [2], 2: [3], 3: [4], 4: [5] })
    expect(p.byMessage.size).toBe(0)
  })

  test('时间正好相等时算在那条消息之后；同一位置的多条记录保持传入顺序', () => {
    const p = placeClassifierLogs(msgs, [at(1, '2026-11-28T10:00:20'), at(2, '2026-11-28T10:00:10'), at(3, '2026-11-28T10:00:10')])
    expect(after(p)).toStrictEqual({ 1: [2, 3], 2: [1] })
  })

  test('没有任何消息时全部放最前面', () => {
    const p = placeClassifierLogs([], [at(1, '2026-11-28T10:00:00'), at(2, '2026-11-28T10:00:05')])
    expect(ids(p.leading)).toStrictEqual([1, 2])
    expect(p.after.size).toBe(0)
  })

  test('消息缺时间或时间乱序时沿用前面见过的最晚时间，不会打乱二分', () => {
    const odd = [other(1, 'kp', '2026-11-28T10:00:00'), other(2, 'kp', undefined), other(3, 'kp', '2026-11-28T09:00:00'), other(4, 'kp', '2026-11-28T10:00:40'), other(5, 'kp', 'oops')]
    const p = placeClassifierLogs(odd, [at(1, '2026-11-28T10:00:10'), at(2, '2026-11-28T10:00:50')])
    // NOTE: 时间 10:00:10 之前最后一条「不晚于它」的是 3 号（沿用 10:00:00），不是更靠后的 4 号
    expect(after(p)).toStrictEqual({ 3: [1], 5: [2] })
  })

  test('开头的消息缺时间时视为最早，记录照常按之后的有效时间落位', () => {
    const odd = [other(1, 'kp', undefined), other(2, 'kp', '2026-11-28T10:00:20')]
    const p = placeClassifierLogs(odd, [at(1, '2026-11-28T10:00:10'), at(2, '2026-11-28T10:00:30')])
    expect(after(p)).toStrictEqual({ 1: [1], 2: [2] })
  })

  test('记录自己的时间解析不出来时放到最后一条消息之后', () => {
    const p = placeClassifierLogs(msgs, [at(1, ''), at(2, 'not-a-time')])
    expect(after(p)).toStrictEqual({ 4: [1, 2] })
  })

  test('RFC 3339 带时区的时间按真实时刻比较', () => {
    const zoned = [other(1, 'kp', '2026-11-28T10:00:00Z'), other(2, 'kp', '2026-11-28T10:00:20Z')]
    const p = placeClassifierLogs(zoned, [at(1, '2026-11-28T18:00:10+08:00'), at(2, '2026-11-28T18:00:30+08:00'), at(3, '2026-11-28T09:59:59Z')])
    expect(after(p)).toStrictEqual({ 1: [1], 2: [2] })
    expect(ids(p.leading)).toStrictEqual([3])
  })

  test('被拦截和放行的孤儿记录同样处理，已有周期号的输入分类不是孤儿', () => {
    const p = placeClassifierLogs(msgs, [
      log(1, { decision: 'blocked', created_at: '2026-11-28T10:00:10' }),
      log(2, { floor: 2, created_at: '2026-11-28T10:00:10' }),
      route(3, 0, { created_at: '2026-11-28T10:00:10' }),
    ])
    expect(after(p)).toStrictEqual({ 1: [1] })
    expect(byMessage(p)).toStrictEqual({ 4: [2] })
  })
})
