import { describe, expect, test } from 'vitest'
import {
  askNames,
  currentGameTime,
  diceOf,
  directorTiming,
  illustrationsOf,
  isEndedMessage,
  isMyMessage,
  messageError,
  msgKind,
  msgLabel,
  stripLegacyTags,
  timeOf,
  writerText,
} from './messages'
import * as RS from './roomState'

describe('messages', () => {
  test('msgKind：优先用 kind，旧数据按 role 归类', () => {
    expect(msgKind({ kind: 'writer' })).toBe('writer')
    expect(msgKind({ role: 'assistant' })).toBe('legacy')
    expect(msgKind({ role: 'user' })).toBe('player')
    expect(msgKind({ role: 'system' })).toBe('system')
    expect(msgKind(null)).toBe('system')
  })

  test('msgLabel 与 isMyMessage', () => {
    expect(msgLabel({ id: 1, kind: 'player', username: '爱丽丝' })).toBe('爱丽丝')
    expect(msgLabel({ id: 1, kind: 'player' })).toBe('玩家')
    expect(msgLabel({ id: 1, kind: 'system' })).toBe('系统')
    expect(msgLabel({ id: 1, kind: 'writer' })).toBe('剧情')
    expect(msgLabel({ id: 1, kind: 'kp' })).toBe('旁白')
    expect(msgLabel({ id: 1, kind: 'ask' })).toBe('旁白')
    expect(msgLabel({ id: 1, role: 'assistant' })).toBe('KP')

    expect(isMyMessage({ id: 1, kind: 'player', user_id: 2 }, 2)).toBe(true)
    expect(isMyMessage({ id: 1, kind: 'player', user_id: '2' }, 2)).toBe(true)
    expect(isMyMessage({ id: 1, kind: 'player', user_id: 3 }, 2)).toBe(false)
    expect(isMyMessage({ id: 1, kind: 'kp', user_id: 2 }, 2)).toBe(false)
    expect(isMyMessage({ id: 1, kind: 'player' }, 2)).toBe(false)
  })

  test('writerText：pending 用流式文本，其余用落库正文', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 2 })
    room.streams[5] = { text: '雨落' }
    expect(writerText(room, { id: 5, kind: 'writer', status: 'pending', content: '' })).toBe('雨落')
    expect(writerText(room, { id: 6, kind: 'writer', status: 'pending', content: '' })).toBe('')
    expect(writerText(room, { id: 5, kind: 'writer', status: 'done', content: '雨落下了。' })).toBe('雨落下了。')
    expect(writerText(null, { id: 5, kind: 'writer', status: 'pending' })).toBe('')
  })

  test('diceOf / timeOf 复用 roomState 的解析', () => {
    expect(diceOf({ id: 1, meta: { dice: '侦查鉴定: 45' } })).toStrictEqual([
      { what: '侦查', roll: '45', level: '', hidden: false },
    ])
    expect(diceOf(null)).toStrictEqual([])
    expect(timeOf({ id: 1, meta: { time_point: '第2天 08:30' } })?.label).toBe('第2天 08:30')
    expect(timeOf({ id: 1 })).toBeNull()
  })

  test('currentGameTime：取最近一条带时间点的消息', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 2 })
    expect(currentGameTime(room)).toBeNull()
    room.messages.push({ id: 1, meta: { time_point: '第1天 08:00' } })
    room.messages.push({ id: 2, meta: {} })
    room.messages.push({ id: 3, meta: { time_point: '第1天 21:00' } })
    room.messages.push({ id: 4 })
    expect(currentGameTime(room)?.label).toBe('第1天 21:00')
    expect(currentGameTime(null)).toBeNull()
  })

  test('currentGameTime：RFC 时间点转成年月日，event 里的 RFC 时间同样生效', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 2 })
    room.messages.push({ id: 1, kind: 'kp', meta: { time_point: '1924-09-03T21:30' } })
    expect(currentGameTime(room)).toStrictEqual({ raw: '1924-09-03T21:30', label: '1924年9月3日 21:30', elapsed: '' })
    room.messages.push({ id: 2, kind: 'event', meta: { events: [{ type: 'time', time: '1924-09-04T06:00' }] } })
    expect(currentGameTime(room)?.label).toBe('1924年9月4日 06:00')
  })

  test('currentGameTime：event 消息里的时间推进立即生效', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 2 })
    room.messages.push({ id: 1, kind: 'kp', meta: { time_point: '第1天 08:00' } })
    room.messages.push({ id: 2, kind: 'event', meta: { events: [{ type: 'dice', roll: 3 }] } })
    expect(currentGameTime(room)?.label).toBe('第1天 08:00')
    room.messages.push({
      id: 3,
      kind: 'event',
      meta: { events: [{ type: 'time', time: '第1天 09:00(距开局已过1h00m)' }, { type: 'time', time: '第1天 10:00(距开局已过2h00m)' }] },
    })
    expect(currentGameTime(room)?.label).toBe('第1天 10:00')
  })

  test('meta 收窄：点名、结束标记、错误、决策耗时', () => {
    expect(askNames({ id: 1, meta: { ask: { names: ['艾米', 3] } } })).toStrictEqual(['艾米', '3'])
    expect(askNames({ id: 1, meta: { ask: {} } })).toStrictEqual([])
    expect(askNames({ id: 1 })).toStrictEqual([])
    expect(isEndedMessage({ id: 1, meta: { ended: true } })).toBe(true)
    expect(isEndedMessage({ id: 1, meta: {} })).toBe(false)
    expect(messageError({ id: 1, meta: { error: '超时' } })).toBe('超时')
    expect(messageError({ id: 1 })).toBe('')
    expect(directorTiming({ id: 1 })).toBeNull()
    expect(directorTiming({ id: 1, director_elapsed_ms: 4200, director_steps: 3 })).toStrictEqual({ elapsedMs: 4200, steps: 3 })
  })

  test('illustrationsOf：按 slot 收窄配图结果，只有 done 且 hash 合法才给出地址', () => {
    const hash = 'f0'.repeat(32)
    const msg = {
      id: 1,
      kind: 'writer',
      meta: {
        illustrations: [
          { slot: 1, status: 'done', hash },
          { slot: 2, status: 'failed' },
          { slot: 3, status: 'done', hash: 'ZZ' + hash.slice(2) },
          { slot: 4, status: 'done', hash: hash.toUpperCase() },
          { slot: 5, status: 'done' },
          { slot: 6, status: 'failed', hash },
          { slot: '7', status: 'done', hash },
          { slot: 8, status: 'pending', hash },
          null,
        ],
      },
    } as unknown as RS.RoomMessage
    expect(illustrationsOf(msg)).toStrictEqual({
      1: { status: 'done', url: `/api/images/${hash}` },
      2: { status: 'failed' },
      3: { status: 'done' },
      4: { status: 'done' },
      5: { status: 'done' },
      6: { status: 'failed' },
    })
  })

  test('illustrationsOf：没有 meta、meta 没有配图或类型不对时返回空对象', () => {
    expect(illustrationsOf(null)).toStrictEqual({})
    expect(illustrationsOf({ id: 1 })).toStrictEqual({})
    expect(illustrationsOf({ id: 1, meta: {} })).toStrictEqual({})
    expect(illustrationsOf({ id: 1, meta: { illustrations: 'x' as unknown as [] } })).toStrictEqual({})
  })

  test('stripLegacyTags：清掉旧消息正文里混入的内部标签', () => {
    expect(stripLegacyTags('  普通正文  ')).toBe('普通正文')
    expect(stripLegacyTags(null)).toBe('')
    expect(stripLegacyTags('开头<ack>确认</ack>正文')).toBe('开头正文')
    expect(stripLegacyTags('正文<dice>侦查鉴定: 45</dice><time_point>第1天 8:00</time_point>')).toBe('正文')
    expect(stripLegacyTags('前文<image_ref id="1"/>后文')).toBe('前文后文')
    expect(stripLegacyTags('前文<image_ref id="1"></image_ref>后文')).toBe('前文后文')
    expect(stripLegacyTags('正文<writer_pending>没有闭合')).toBe('正文')
    expect(stripLegacyTags('1 < 2 的比较')).toBe('1 < 2 的比较')
  })
})
