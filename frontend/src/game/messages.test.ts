import { describe, expect, test } from 'vitest'
import {
  askNames,
  currentGameTime,
  diceOf,
  directorTiming,
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
    expect(msgLabel({ id: 1, kind: 'writer' })).toBe('KP')

    expect(isMyMessage({ id: 1, kind: 'player', user_id: 2 }, 2)).toBe(true)
    expect(isMyMessage({ id: 1, kind: 'player', user_id: '2' }, 2)).toBe(true)
    expect(isMyMessage({ id: 1, kind: 'player', user_id: 3 }, 2)).toBe(false)
    expect(isMyMessage({ id: 1, kind: 'kp', user_id: 2 }, 2)).toBe(false)
    expect(isMyMessage({ id: 1, kind: 'player' }, 2)).toBe(false)
  })

  test('writerText：pending 用流式文本，其余用落库正文', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 2 })
    room.streams[5] = { text: '雨落', live: true, broken: false }
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
