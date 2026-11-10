import { describe, expect, test } from 'vitest'
import { continueLabel, hasKPStarted, playerTurnSubmitState, roomBanner, showWaiting, waitedLabel } from './banner'
import * as RS from './roomState'
import type { Room } from './roomState'

function playingRoom(userId = 2): Room {
  const room = RS.newRoom({ sessionId: 7, userId })
  RS.onHello(room, { epoch: 'E1', seq: 1 })
  RS.applySnapshot(room, { session: { status: 'playing' }, messages: [], chatStatus: {} })
  return room
}

describe('waitedLabel', () => {
  test('周期运行中按 mm:ss 显示，没有开始时间或周期未运行时为空', () => {
    const room = playingRoom()
    expect(waitedLabel(room, 1_000_000)).toBe('')
    room.floor.running = true
    expect(waitedLabel(room, 1_000_000)).toBe('')
    room.floor.startedAt = 1_000_000 - 75_000
    expect(waitedLabel(room, 1_000_000)).toBe('已等待 01:15')
    room.floor.startedAt = 1_000_000 + 5_000
    expect(waitedLabel(room, 1_000_000)).toBe('已等待 00:00')
    room.floor.running = false
    expect(waitedLabel(room, 1_000_000)).toBe('')
    expect(waitedLabel(null, 1_000_000)).toBe('')
  })
})

describe('roomBanner', () => {
  test('没有房间时为空', () => {
    expect(roomBanner(null)).toStrictEqual({ text: '', tone: 'idle', busy: false })
  })

  test('未连上时按连接状态给出提示', () => {
    const room = playingRoom()
    room.conn = 'connecting'
    expect(roomBanner(room)).toStrictEqual({ text: '正在连接…', tone: 'warn', busy: true })
    room.conn = 'snapshot'
    expect(roomBanner(room).text).toBe('正在同步房间状态…')
    room.conn = 'reconnecting'
    expect(roomBanner(room).text).toBe('连接中断，正在重连…')
  })

  test('房间状态：结束、未开始', () => {
    const room = playingRoom()
    room.status = 'ended'
    expect(roomBanner(room)).toStrictEqual({ text: '游戏已结束', tone: 'idle', busy: false })
    room.status = 'lobby'
    expect(roomBanner(room)).toStrictEqual({ text: '等待房主开始游戏', tone: 'idle', busy: false })
  })

  test('周期运行中优先显示 KP 状态文案，没有则用默认文案；启动中单独提示', () => {
    const room = playingRoom()
    room.floor.running = true
    expect(roomBanner(room).text).toBe('KP 正在推进剧情…')
    room.kpStatus = 'KP 正在整理记忆'
    expect(roomBanner(room)).toStrictEqual({ text: 'KP 正在整理记忆', tone: 'info', busy: true })
    room.floor.running = false
    room.floor.starting = true
    expect(roomBanner(room)).toStrictEqual({ text: 'KP 正在启动…', tone: 'info', busy: true })
  })

  test('KP 暂停：已知原因给出中文，未知原因原样显示', () => {
    const room = playingRoom()
    for (const [stop, why] of [
      ['host_stop', '房主打断'],
      ['loop', '检测到重复操作'],
      ['stall', '长时间没有进展'],
      ['error', '出错'],
      ['context', '上下文过长'],
      ['mystery', 'mystery'],
    ]) {
      room.spotlight.stop = stop
      expect(roomBanner(room).text).toBe('KP 已暂停：' + why + '，等待房主让 KP 继续')
    }
    expect(roomBanner(room).busy).toBe(false)
  })

  test('被点名：未提交提示回应，提交后显示还在等谁', () => {
    const room = playingRoom()
    room.spotlight.askedMe = true
    room.spotlight.userIds = [2]
    expect(roomBanner(room)).toStrictEqual({ text: 'KP 在等你回应', tone: 'me', busy: false })
    room.me.submitted = true
    expect(roomBanner(room).text).toBe('已提交行动，等待 KP')
    room.waiting.pendingNames = ['鲍勃', '凯特']
    expect(roomBanner(room).text).toBe('已提交行动，还在等 鲍勃、凯特')
  })

  test('他人被点名：显示名字与多人时的提交进度', () => {
    const room = playingRoom()
    room.spotlight.userIds = [3]
    expect(roomBanner(room).text).toBe('KP 在等 玩家 回应')
    room.spotlight.names = ['鲍勃', '凯特']
    room.waiting.total = 2
    room.waiting.pending = 1
    expect(roomBanner(room).text).toBe('KP 在等 鲍勃、凯特 回应（1/2 已提交）')
  })

  test('KP 持有发言权时区分开场和继续', () => {
    const room = playingRoom()
    expect(roomBanner(room).text).toBe('等待房主让 KP 开场')
    expect(continueLabel(room)).toBe('让 KP 开场')
    room.messages.push({ id: 1, kind: 'event', status: 'done', floor: 1, meta: { events: [{ type: 'dice', hidden: true }] } })
    expect(hasKPStarted(room)).toBe(false)
    room.messages.push({ id: 2, kind: 'kp', status: 'done', floor: 1 })
    expect(hasKPStarted(room)).toBe(true)
    expect(roomBanner(room).text).toBe('等待房主让 KP 继续')
    expect(continueLabel(room)).toBe('让 KP 继续')
    expect(hasKPStarted(null)).toBe(false)
  })

  test('房主看到的是可点击的操作提示，而不是在等自己', () => {
    const room = playingRoom()
    room.isHost = true
    expect(roomBanner(room)).toStrictEqual({ text: '请点击「让 KP 开场」', tone: 'me', busy: false })
    room.messages.push({ id: 2, kind: 'kp', status: 'done', floor: 1 })
    expect(roomBanner(room).text).toBe('请点击「让 KP 继续」')

    room.spotlight.stop = 'loop'
    expect(roomBanner(room).text).toBe('KP 已暂停：检测到重复操作，请点击「让 KP 继续」')

    room.status = 'lobby'
    expect(roomBanner(room)).toStrictEqual({ text: '人齐后点击上方「开始」启动游戏', tone: 'idle', busy: false })
  })

  test('房主被点名时仍优先提示回应', () => {
    const room = playingRoom()
    room.isHost = true
    room.spotlight.askedMe = true
    room.spotlight.userIds = [2]
    expect(roomBanner(room).text).toBe('KP 在等你回应')
  })
})

describe('showWaiting / playerTurnSubmitState', () => {
  test('只有多人点名且部分已提交时才显示等待进度', () => {
    const room = playingRoom()
    expect(showWaiting(room)).toBe(false)
    room.waiting = { pending: 1, total: 2, submittedNames: ['爱丽丝'], pendingNames: ['鲍勃'] }
    expect(showWaiting(room)).toBe(true)
    room.waiting.pending = 2
    expect(showWaiting(room)).toBe(false)
    room.waiting.pending = 1
    room.floor.running = true
    expect(showWaiting(room)).toBe(false)
    expect(showWaiting(null)).toBe(false)
  })

  test('按调查员名字（trim 后）匹配已提交与未提交', () => {
    const room = playingRoom()
    room.waiting = { pending: 1, total: 2, submittedNames: [' 爱丽丝 '], pendingNames: ['鲍勃'] }
    expect(playerTurnSubmitState(room, { character_card: { name: '爱丽丝' } })).toBe('submitted')
    expect(playerTurnSubmitState(room, { character_card: { name: ' 鲍勃' } })).toBe('pending')
    expect(playerTurnSubmitState(room, { user: { username: '鲍勃' } })).toBe('pending')
    expect(playerTurnSubmitState(room, { character_card: { name: '凯特' } })).toBeNull()
    expect(playerTurnSubmitState(room, {})).toBeNull()
    expect(playerTurnSubmitState(room, null)).toBeNull()
    room.waiting.pending = 0
    expect(playerTurnSubmitState(room, { character_card: { name: '爱丽丝' } })).toBeNull()
  })
})
