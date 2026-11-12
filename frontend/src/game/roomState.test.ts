import { reactive, watchEffect } from 'vue'
import { describe, expect, test } from 'vitest'
import * as RS from './roomState'
import type { Frame, FrameData, Gates, Room, RoomMessage, Snapshot } from './roomState'

const F = (seq: number, type: string, data: FrameData = {}): Frame => ({ seq, type, data })
const msg = (id: number, extra: Partial<RoomMessage> = {}): RoomMessage => ({
  id,
  role: 'assistant',
  kind: 'kp',
  status: 'done',
  content: '',
  ...extra,
})
const writer = (id: number, extra: Partial<RoomMessage> = {}): RoomMessage =>
  msg(id, { kind: 'writer', status: 'pending', ...extra })

// NOTE: 约定的调用顺序：acceptFrame 只做分类并推进游标，返回 'apply' 时调用方再调 applyFrame
// NOTE: hello 之后的 resync 帧同样返回 'apply'，由 applyFrame 完成 helloPending -> snapshot 的切换并返回 ['fetchSnapshot']
// NOTE: 只有 live 下出现跳号（或 live 下收到 resync）才返回 'resync'，调用方应 resetCursor 后重连
function feed(room: Room, frame: Frame) {
  const verdict = RS.acceptFrame(room, frame)
  const effects = verdict === 'apply' ? RS.applyFrame(room, frame) : []
  return { verdict, effects }
}

function snap(extra: Snapshot = {}): Snapshot {
  return { session: { status: 'playing' }, messages: [], chatStatus: {}, ...extra }
}

function liveRoom({ seq = 10, userId = 3, isHost = false } = {}): Room {
  const room = RS.newRoom({ sessionId: 1, userId, isHost })
  RS.onHello(room, { epoch: 'e1', seq })
  RS.applySnapshot(room, snap())
  return room
}

describe('roomState', () => {
  test('newRoom 初始状态', () => {
    const room = RS.newRoom({ sessionId: 9, userId: 3, isHost: true })
    expect(room.sessionId).toBe(9)
    expect(room.userId).toBe(3)
    expect(room.isHost).toBe(true)
    expect(room.epoch).toBe('')
    expect(room.lastSeq).toBe(0)
    expect(room.conn).toBe('connecting')
    expect(room.status).toBe('lobby')
    expect(room.floor).toStrictEqual({ running: false, number: 0, starting: false, startedAt: 0 })
    expect(room.me).toStrictEqual({ submitted: false, draft: '' })
  })

  test('首次连接：hello 后补发的 seq 1..5 全部丢弃，快照期间的 6、7 缓冲后依次应用', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 3, isHost: false })
    expect(RS.onHello(room, { epoch: 'e1', seq: 5 })).toStrictEqual(['fetchSnapshot'])
    expect(room.conn).toBe('snapshot')
    expect(room.baseSeq).toBe(5)
    expect(room.epoch).toBe('e1')

    for (let s = 1; s <= 5; s++) {
      expect(RS.acceptFrame(room, F(s, 'message_new', { message: msg(100 + s) }))).toBe('drop')
    }
    expect(room.messages.length).toBe(0)
    expect(RS.acceptFrame(room, F(0, 'kp_status', { text: '思考中' }))).toBe('drop')

    expect(
      RS.acceptFrame(
        room,
        F(6, 'spotlight', { user_ids: [3], names: ['我'], question: '开门吗', kp_floor: false, stop: '' }),
      ),
    ).toBe('buffer')
    expect(RS.acceptFrame(room, F(7, 'message_new', { message: msg(106, { kind: 'ask' }) }))).toBe('buffer')
    expect(room.pending.length).toBe(2)
    expect(room.lastSeq).toBe(0)
    expect(room.spotlight.askedMe).toBe(false)

    const effects = RS.applySnapshot(room, snap({ messages: [msg(101), msg(102), msg(103), msg(104), msg(105)] }))
    expect(effects).toStrictEqual([])
    expect(room.messages.map((m) => m.id)).toStrictEqual([101, 102, 103, 104, 105, 106])
    expect(room.spotlight.askedMe).toBe(true)
    expect(room.spotlight.question).toBe('开门吗')
    expect(room.lastSeq).toBe(7)
    expect(room.pending.length).toBe(0)
    expect(room.conn).toBe('live')
    expect(RS.acceptFrame(room, F(8, 'waiting', {}))).toBe('apply')
    expect(room.lastSeq).toBe(8)
  })

  test('快照期间到达的帧乱序或重复时，按 seq 去重排序后应用', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 3 })
    RS.onHello(room, { epoch: 'e1', seq: 5 })
    RS.acceptFrame(room, F(7, 'kp_status', { text: 'x' }))
    RS.acceptFrame(room, F(7, 'floor_start', { floor: 2 }))
    RS.acceptFrame(room, F(6, 'floor_start', { floor: 1 }))
    RS.applySnapshot(room, snap())
    expect(room.floor.number).toBe(2)
    expect(room.lastSeq).toBe(7)
  })

  test('没有缓冲帧时，快照应用后 lastSeq 等于 hello.seq', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 3 })
    RS.onHello(room, { epoch: 'e1', seq: 42 })
    RS.applySnapshot(room, snap())
    expect(room.lastSeq).toBe(42)
    expect(room.conn).toBe('live')
  })

  test('onHello 同时接受整帧和仅 data 两种入参', () => {
    const a = RS.newRoom({ sessionId: 1, userId: 3 })
    RS.onHello(a, { seq: 0, type: 'hello', data: { epoch: 'e9', seq: 12 } })
    expect(a.baseSeq).toBe(12)
    expect(a.epoch).toBe('e9')
  })

  test('resync：hello 后紧跟 resync，进入 snapshot，用快照整体替换，lastSeq=hello.seq', () => {
    const room = liveRoom({ seq: 10 })
    feed(room, F(11, 'message_new', { message: msg(500) }))
    expect(room.lastSeq).toBe(11)
    RS.markDisconnected(room)

    expect(RS.onHello(room, { epoch: 'e1', seq: 40 })).toStrictEqual([])
    expect(room.conn).toBe('helloPending')

    const r = feed(room, F(0, 'resync'))
    expect(r.verdict).toBe('apply')
    expect(r.effects).toStrictEqual(['fetchSnapshot'])
    expect(room.conn).toBe('snapshot')
    expect(room.lastSeq).toBe(0)

    expect(RS.acceptFrame(room, F(12, 'waiting', {}))).toBe('drop')
    expect(RS.acceptFrame(room, F(40, 'waiting', {}))).toBe('drop')
    expect(RS.acceptFrame(room, F(41, 'message_new', { message: msg(601) }))).toBe('buffer')

    RS.applySnapshot(room, snap({ messages: [msg(600)] }))
    expect(room.messages.map((m) => m.id)).toStrictEqual([600, 601])
    expect(room.byId[500]).toBeUndefined()
    expect(room.lastSeq).toBe(41)
    expect(room.conn).toBe('live')
  })

  test('resync：snapshot 期间再收到 resync 帧直接丢弃', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 3 })
    RS.onHello(room, { epoch: 'e1', seq: 5 })
    expect(RS.acceptFrame(room, F(0, 'resync'))).toBe('drop')
    expect(room.conn).toBe('snapshot')
  })

  test('resync：live 下收到 resync 帧返回 resync，不改状态', () => {
    const room = liveRoom({ seq: 10 })
    expect(RS.acceptFrame(room, F(0, 'resync'))).toBe('resync')
    expect(room.conn).toBe('live')
    expect(room.lastSeq).toBe(10)
  })

  test('epoch 变化与 resync 走同一条路：立即 snapshot，旧 lastSeq 不影响新 epoch 的帧', () => {
    const room = liveRoom({ seq: 10 })
    RS.markDisconnected(room)
    expect(RS.onHello(room, { epoch: 'e2', seq: 3 })).toStrictEqual(['fetchSnapshot'])
    expect(room.conn).toBe('snapshot')
    expect(room.epoch).toBe('e2')

    expect(RS.acceptFrame(room, F(3, 'waiting', {}))).toBe('drop')
    expect(RS.acceptFrame(room, F(4, 'floor_start', { floor: 1 }))).toBe('buffer')
    RS.applySnapshot(room, snap())
    expect(room.lastSeq).toBe(4)
    expect(room.floor.running).toBe(true)
  })

  test('epoch 变化后快照没拉成功就断线，下次重连仍然走快照', () => {
    const room = liveRoom({ seq: 10 })
    RS.markDisconnected(room)
    RS.onHello(room, { epoch: 'e2', seq: 3 })
    RS.markDisconnected(room)
    expect(RS.onHello(room, { epoch: 'e2', seq: 5 })).toStrictEqual(['fetchSnapshot'])
    expect(room.conn).toBe('snapshot')
  })

  test('主动 resync：resetCursor 清游标和缓冲，保留 epoch，下次 hello 走快照', () => {
    const room = liveRoom({ seq: 10 })
    room.pending.push(F(99, 'waiting'))
    RS.resetCursor(room)
    expect(room.lastSeq).toBe(0)
    expect(room.pending.length).toBe(0)
    expect(room.conn).toBe('connecting')
    expect(room.epoch).toBe('e1')
    expect(RS.onHello(room, { epoch: 'e1', seq: 20 })).toStrictEqual(['fetchSnapshot'])
  })

  test('普通重连：hello 后补发帧，seq<=lastSeq 丢弃，其余按序应用，live 下跳号返回 resync', () => {
    const room = liveRoom({ seq: 10 })
    RS.markDisconnected(room)
    expect(RS.onHello(room, { epoch: 'e1', seq: 14 })).toStrictEqual([])
    expect(room.conn).toBe('helloPending')
    expect(room.baseSeq).toBe(14)

    expect(feed(room, F(10, 'message_new', { message: msg(1) })).verdict).toBe('drop')
    expect(room.conn).toBe('live')
    expect(room.messages.length).toBe(0)

    expect(feed(room, F(11, 'message_new', { message: msg(2) })).verdict).toBe('apply')
    expect(feed(room, F(12, 'message_new', { message: msg(3) })).verdict).toBe('apply')
    expect(room.lastSeq).toBe(12)
    expect(feed(room, F(12, 'message_new', { message: msg(3) })).verdict).toBe('drop')
    expect(room.messages.map((m) => m.id)).toStrictEqual([2, 3])

    expect(RS.acceptFrame(room, F(14, 'message_new', { message: msg(5) }))).toBe('resync')
    expect(room.lastSeq).toBe(12)
    expect(room.byId[5]).toBeUndefined()

    expect(feed(room, F(13, 'message_new', { message: msg(4) })).verdict).toBe('apply')
    expect(room.lastSeq).toBe(13)
  })

  test('普通重连：hello 之后没有帧就一直停在 helloPending，且 gates 视为 live', () => {
    const room = liveRoom({ seq: 10, isHost: true })
    RS.markDisconnected(room)
    RS.onHello(room, { epoch: 'e1', seq: 10 })
    expect(room.conn).toBe('helloPending')
    expect(RS.gates(room).live).toBe(true)
    expect(RS.gates(room).canContinue).toBe(true)
  })

  test('普通重连：hello 后第一帧是临时帧也确认为 live', () => {
    const room = liveRoom({ seq: 10 })
    RS.markDisconnected(room)
    RS.onHello(room, { epoch: 'e1', seq: 10 })
    const r = feed(room, F(0, 'kp_status', { text: '思考中' }))
    expect(r.verdict).toBe('apply')
    expect(room.conn).toBe('live')
    expect(room.kpStatus).toBe('思考中')
    expect(room.lastSeq).toBe(10)
  })

  test('临时帧（seq=0）不推进 lastSeq，snapshot 下丢弃', () => {
    const room = liveRoom({ seq: 10 })
    expect(feed(room, F(0, 'kp_status', { text: 'a' })).verdict).toBe('apply')
    expect(room.lastSeq).toBe(10)
    RS.resetCursor(room)
    RS.onHello(room, { epoch: 'e1', seq: 10 })
    expect(RS.acceptFrame(room, F(0, 'kp_status', { text: 'b' }))).toBe('drop')
    expect(room.kpStatus).toBe('a')
  })

  test('connecting / reconnecting 状态下收到的帧一律丢弃', () => {
    const room = liveRoom({ seq: 10 })
    RS.markDisconnected(room)
    expect(RS.acceptFrame(room, F(11, 'waiting', {}))).toBe('drop')
    const fresh = RS.newRoom({ sessionId: 1, userId: 3 })
    expect(RS.acceptFrame(fresh, F(1, 'waiting', {}))).toBe('drop')
  })

  test('message_new 重放不会把已 done 的消息打回 pending', () => {
    const room = liveRoom()
    feed(room, F(11, 'message_new', { message: writer(7) }))
    feed(room, F(12, 'message_update', { id: 7, status: 'done', content: '终稿' }))
    expect(room.byId[7].status).toBe('done')

    RS.applyFrame(room, F(11, 'message_new', { message: writer(7) }))
    expect(room.byId[7].status).toBe('done')
    expect(room.byId[7].content).toBe('终稿')
    expect(room.streams[7]).toBeUndefined()
    expect(room.messages.length).toBe(1)

    RS.applyFrame(room, F(13, 'message_new', { message: writer(8, { status: 'failed' }) }))
    RS.applyFrame(room, F(13, 'message_new', { message: writer(8) }))
    expect(room.byId[8].status).toBe('failed')
  })

  test('快照与缓冲帧重叠时收敛：快照里已 done 的 writer 不会被缓冲的 message_new 打回', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 3 })
    RS.onHello(room, { epoch: 'e1', seq: 5 })
    RS.acceptFrame(room, F(6, 'message_new', { message: writer(20) }))
    RS.acceptFrame(room, F(7, 'message_update', { id: 20, status: 'done', content: '全文' }))
    RS.applySnapshot(room, snap({ messages: [writer(20, { status: 'done', content: '全文' })] }))
    expect(room.byId[20].status).toBe('done')
    expect(room.byId[20].content).toBe('全文')
    expect(room.streams[20]).toBeUndefined()
    expect(room.messages.length).toBe(1)
    expect(room.lastSeq).toBe(7)
  })

  test('消息按 id 升序 upsert，byId 与 messages 指向同一对象', () => {
    const room = liveRoom()
    RS.applyFrame(room, F(11, 'message_new', { message: msg(30, { content: 'c' }) }))
    RS.applyFrame(room, F(12, 'message_new', { message: msg(10) }))
    RS.applyFrame(room, F(13, 'message_new', { message: msg(20) }))
    RS.applyFrame(room, F(14, 'message_new', { message: msg(20, { content: '新' }) }))
    expect(room.messages.map((m) => m.id)).toStrictEqual([10, 20, 30])
    expect(room.byId[20]).toBe(room.messages[1])
    expect(room.byId[20].content).toBe('新')
  })

  test('message_update 的 content 覆盖本地流式文本并删除 stream，error 写入 meta', () => {
    const room = liveRoom()
    feed(room, F(11, 'message_new', { message: writer(7) }))
    feed(room, F(0, 'writer_token', { message_id: 7, text: '雨' }))
    feed(room, F(0, 'writer_token', { message_id: 7, text: '夜' }))
    expect(room.streams[7].text).toBe('雨夜')

    feed(room, F(12, 'message_update', { id: 7, status: 'done', content: '雨夜，钟声响了。' }))
    expect(room.byId[7].content).toBe('雨夜，钟声响了。')
    expect(room.byId[7].status).toBe('done')
    expect(room.streams[7]).toBeUndefined()

    feed(room, F(13, 'message_new', { message: writer(9, { meta: { nsfw: true } }) }))
    feed(room, F(14, 'message_update', { id: 9, status: 'failed', error: '额度不足' }))
    expect(room.byId[9].status).toBe('failed')
    expect(room.byId[9].meta?.error).toBe('额度不足')
    expect(room.byId[9].meta?.nsfw).toBe(true)
  })

  test('配图画完才以 done 的 message_new 到达，排在已有消息之后', () => {
    const room = liveRoom()
    feed(room, F(11, 'message_new', { message: msg(20, { content: '先到的 KP 回复' }) }))
    feed(room, F(12, 'message_new', { message: msg(21, { kind: 'image', images: ['/api/images/a.png'] }) }))
    expect(room.messages.map((m) => m.id)).toStrictEqual([20, 21])
    expect(room.byId[21].status).toBe('done')
    expect(room.byId[21].images).toStrictEqual(['/api/images/a.png'])
  })

  test('message_update 先于 message_new 到达时不报错也不残留 stream', () => {
    const room = liveRoom()
    expect(RS.applyFrame(room, F(11, 'message_update', { id: 99, status: 'done', content: 'x' }))).toStrictEqual([])
    expect(room.byId[99]).toBeUndefined()
    expect(room.messages.length).toBe(0)
  })

  test('writer 流：message_new 之前到达的 token 也先记下，已有终态的消息不再接收', () => {
    const room = liveRoom()
    feed(room, F(0, 'writer_token', { message_id: 7, offset: 0, text: '早' }))
    expect(room.streams[7]).toStrictEqual({ text: '早' })

    feed(room, F(11, 'message_new', { message: writer(7) }))
    feed(room, F(0, 'writer_token', { message_id: 7, offset: 1, text: '雨' }))
    expect(room.streams[7].text).toBe('早雨')

    feed(room, F(12, 'message_update', { id: 7, status: 'done', content: '早雨。' }))
    feed(room, F(0, 'writer_token', { message_id: 7, offset: 2, text: '晚' }))
    expect(room.streams[7]).toBeUndefined()
  })

  test('writer 流：按偏移拼接，重叠部分覆盖、重复帧忽略、跳过的帧等补发', () => {
    const room = liveRoom()
    feed(room, F(11, 'message_new', { message: writer(7) }))
    feed(room, F(0, 'writer_token', { message_id: 7, offset: 0, text: '雨夜' }))
    feed(room, F(0, 'writer_token', { message_id: 7, offset: 2, text: '，钟' }))
    expect(room.streams[7].text).toBe('雨夜，钟')

    feed(room, F(0, 'writer_token', { message_id: 7, offset: 2, text: '，钟' }))
    expect(room.streams[7].text).toBe('雨夜，钟')

    feed(room, F(0, 'writer_token', { message_id: 7, offset: 9, text: '丢了前面' }))
    expect(room.streams[7].text).toBe('雨夜，钟')

    feed(room, F(0, 'writer_token', { message_id: 7, offset: 0, text: '雨夜，钟声响了' }))
    expect(room.streams[7].text).toBe('雨夜，钟声响了')
    feed(room, F(0, 'writer_token', { message_id: 7, offset: 4, text: '声响了。' }))
    expect(room.streams[7].text).toBe('雨夜，钟声响了。')
  })

  test('writer 流：快照期间收到的全文补发不会被快照清掉，之后的增量接着拼', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 3 })
    RS.onHello(room, { epoch: 'e1', seq: 5 })
    expect(feed(room, F(0, 'writer_token', { message_id: 7, offset: 0, text: '前半' })).verdict).toBe('apply')
    RS.applySnapshot(room, snap({ messages: [writer(7)] }))
    expect(room.streams[7].text).toBe('前半')

    feed(room, F(0, 'writer_token', { message_id: 7, offset: 2, text: '后半' }))
    expect(room.streams[7].text).toBe('前半后半')

    feed(room, F(6, 'message_update', { id: 7, status: 'done', content: '完整正文' }))
    expect(room.byId[7].content).toBe('完整正文')
    expect(room.streams[7]).toBeUndefined()
  })

  test('writer 流：writer_reset 清掉半截正文，之后的 token 从 offset=0 重新拼', () => {
    const room = liveRoom()
    feed(room, F(11, 'message_new', { message: writer(7) }))
    feed(room, F(0, 'writer_token', { message_id: 7, offset: 0, text: '雨夜，钟' }))
    expect(room.streams[7].text).toBe('雨夜，钟')

    feed(room, F(0, 'writer_reset', { message_id: 7 }))
    expect(room.streams[7]).toBeUndefined()

    feed(room, F(0, 'writer_token', { message_id: 7, offset: 0, text: '雨' }))
    expect(room.streams[7].text).toBe('雨')
    feed(room, F(0, 'writer_token', { message_id: 7, offset: 1, text: '夜里' }))
    expect(room.streams[7].text).toBe('雨夜里')

    feed(room, F(0, 'writer_reset', { message_id: 99 }))
    feed(room, F(0, 'writer_reset', {}))
    expect(room.streams[7].text).toBe('雨夜里')
  })

  test('writer 流：快照期间也接收 writer_reset，不会漏掉重试的信号', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 3 })
    RS.onHello(room, { epoch: 'e1', seq: 5 })
    feed(room, F(0, 'writer_token', { message_id: 7, offset: 0, text: '半截' }))
    expect(feed(room, F(0, 'writer_reset', { message_id: 7 })).verdict).toBe('apply')
    expect(room.streams[7]).toBeUndefined()
  })

  test('writer 流：快照里已经完成的 writer 会清掉残留的流', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 3 })
    RS.onHello(room, { epoch: 'e1', seq: 5 })
    feed(room, F(0, 'writer_token', { message_id: 7, offset: 0, text: '半截' }))
    RS.applySnapshot(room, snap({ messages: [writer(7, { status: 'done', content: '全文' })] }))
    expect(room.streams[7]).toBeUndefined()
  })

  test('writer 流：断线重连后由全文补发接上，message_update 覆盖', () => {
    const room = liveRoom()
    feed(room, F(11, 'message_new', { message: writer(7) }))
    feed(room, F(0, 'writer_token', { message_id: 7, offset: 0, text: '雨' }))
    room.kpStatus = 'KP 正在思考…'

    RS.markDisconnected(room)
    expect(room.conn).toBe('reconnecting')
    expect(room.kpStatus).toBe('')
    expect(room.streams[7]).toStrictEqual({ text: '雨' })

    RS.onHello(room, { epoch: 'e1', seq: 12 })
    RS.applyFrame(room, F(0, 'writer_token', { message_id: 7, offset: 0, text: '雨夜里' }))
    expect(room.streams[7].text).toBe('雨夜里')

    feed(room, F(12, 'message_update', { id: 7, status: 'done', content: '雨夜' }))
    expect(room.byId[7].content).toBe('雨夜')
    expect(room.streams[7]).toBeUndefined()
  })

  test('message_new：ask / kp 清空 me.submitted，自己的 player 消息清空 draft', () => {
    const room = liveRoom({ userId: 3 })
    room.me.submitted = true
    room.me.draft = '我推门'
    RS.applyFrame(room, F(11, 'message_new', { message: msg(1, { kind: 'player', user_id: 4 }) }))
    expect(room.me.draft).toBe('我推门')
    expect(room.me.submitted).toBe(true)
    RS.applyFrame(room, F(12, 'message_new', { message: msg(2, { kind: 'player', user_id: 3 }) }))
    expect(room.me.draft).toBe('')
    expect(room.me.submitted).toBe(true)
    RS.applyFrame(room, F(13, 'message_new', { message: msg(3, { kind: 'ask' }) }))
    expect(room.me.submitted).toBe(false)

    room.me.submitted = true
    RS.applyFrame(room, F(14, 'message_new', { message: msg(4, { kind: 'kp' }) }))
    expect(room.me.submitted).toBe(false)
  })

  test('message_new 重放不会再次清空 me.submitted', () => {
    const room = liveRoom()
    RS.applyFrame(room, F(11, 'message_new', { message: msg(3, { kind: 'ask' }) }))
    room.me.submitted = true
    RS.applyFrame(room, F(11, 'message_new', { message: msg(3, { kind: 'ask' }) }))
    expect(room.me.submitted).toBe(true)
  })

  test('spotlight：字段映射，askedMe 由 userIds 推出，不再被点名则清空 me.submitted', () => {
    const room = liveRoom({ userId: 3 })
    room.me.submitted = true
    RS.applyFrame(
      room,
      F(11, 'spotlight', { user_ids: [3, 4], names: ['我', '鲍勃'], question: '怎么开门？', kp_floor: false, stop: '' }),
    )
    expect(room.spotlight).toStrictEqual({
      userIds: [3, 4],
      names: ['我', '鲍勃'],
      question: '怎么开门？',
      kpFloor: false,
      askedMe: true,
      stop: '',
    })
    expect(room.me.submitted).toBe(true)

    RS.applyFrame(room, F(12, 'spotlight', { user_ids: [4], names: ['鲍勃'], question: 'q', kp_floor: false, stop: '' }))
    expect(room.spotlight.askedMe).toBe(false)
    expect(room.me.submitted).toBe(false)

    RS.applyFrame(room, F(13, 'spotlight', { user_ids: [], names: [], question: '', kp_floor: true, stop: 'host_stop' }))
    expect(room.spotlight.userIds).toStrictEqual([])
    expect(room.spotlight.kpFloor).toBe(true)
    expect(room.spotlight.stop).toBe('host_stop')
  })

  test('waiting：设值；被点名且未提交时返回 fetchChatStatus', () => {
    const room = liveRoom({ userId: 3 })
    const payload = { pending: 1, total: 2, submitted_names: ['爱丽丝'], pending_names: ['我'] }

    expect(RS.applyFrame(room, F(11, 'waiting', payload))).toStrictEqual([])
    expect(room.waiting).toStrictEqual({ pending: 1, total: 2, submittedNames: ['爱丽丝'], pendingNames: ['我'] })

    RS.applyFrame(room, F(12, 'spotlight', { user_ids: [3], names: ['我'], question: '', kp_floor: false, stop: '' }))
    expect(RS.applyFrame(room, F(13, 'waiting', payload))).toStrictEqual(['fetchChatStatus'])
    expect(RS.applyFrame(room, F(13, 'waiting', payload))).toStrictEqual(['fetchChatStatus'])
    expect(room.waiting.pending).toBe(1)

    room.me.submitted = true
    expect(
      RS.applyFrame(room, F(14, 'waiting', { pending: 0, total: 2, submitted_names: ['爱丽丝', '我'], pending_names: [] })),
    ).toStrictEqual([])
    expect(room.waiting.pending).toBe(0)
  })

  test('applyChatStatus：只覆盖周期、发言权、等待进度和已提交，不动消息和游标', () => {
    const room = liveRoom({ userId: 3, isHost: true })
    RS.applyFrame(room, F(11, 'message_new', { message: { id: 1, kind: 'kp', status: 'done', content: 'x' } }))
    room.floor.starting = true
    const seqBefore = room.lastSeq

    RS.applyChatStatus(room, {
      kp_running: false,
      floor: null,
      submitted: true,
      spotlight: { user_ids: [3, 4], names: ['我', '鲍勃'], question: '怎么办', kp_floor: false, asked_me: true, stop: '' },
      waiting: { pending: 1, total: 2, submitted_names: ['我'], pending_names: ['鲍勃'] },
    })

    expect(room.floor.running).toBe(false)
    expect(room.floor.starting).toBe(false)
    expect(room.spotlight.askedMe).toBe(true)
    expect(room.me.submitted).toBe(true)
    expect(room.waiting.pendingNames).toStrictEqual(['鲍勃'])
    expect(room.messages.length).toBe(1)
    expect(room.lastSeq).toBe(seqBefore)

    RS.applyChatStatus(room, { kp_running: true, floor: { floor: 5 }, spotlight: {}, waiting: {} })
    expect(room.floor.running).toBe(true)
    expect(room.floor.number).toBe(5)
  })

  test('applyChatStatus：重连后用服务器记录的原文回填草稿，已有本地草稿不覆盖', () => {
    const room = liveRoom({ userId: 3 })
    RS.applyChatStatus(room, { submitted: true, submitted_content: '我推门', spotlight: {}, waiting: {} })
    expect(room.me.draft).toBe('我推门')

    room.me.draft = '我踹门'
    RS.applyChatStatus(room, { submitted: true, submitted_content: '我推门', spotlight: {}, waiting: {} })
    expect(room.me.draft).toBe('我踹门')

    const fresh = liveRoom({ userId: 3 })
    RS.applyChatStatus(fresh, { submitted: false, submitted_content: '', spotlight: {}, waiting: {} })
    expect(fresh.me.draft).toBe('')
  })

  test('applyChatStatus：周期开始时间取服务器的 started_at，周期结束后归零', () => {
    const room = liveRoom({ userId: 3 })
    RS.applyChatStatus(room, { floor: { floor: 2, started_at: '2026-11-10T06:00:00Z' }, spotlight: {}, waiting: {} })
    expect(room.floor.startedAt).toBe(Date.parse('2026-11-10T06:00:00Z'))

    RS.applyChatStatus(room, { floor: null, kp_running: false, spotlight: {}, waiting: {} })
    expect(room.floor.startedAt).toBe(0)
  })

  test('floor_start 记录本地开始时间，floor_end 清零', () => {
    const room = liveRoom({ userId: 3 })
    RS.applyFrame(room, F(11, 'floor_start', { floor: 3, reason: 'player' }))
    expect(room.floor.startedAt).toBeGreaterThan(0)
    RS.applyFrame(room, F(12, 'floor_end', { floor: 3 }))
    expect(room.floor.startedAt).toBe(0)
  })

  test('floor_start 清空上一轮的等待进度', () => {
    const room = liveRoom({ userId: 3 })
    RS.applyFrame(room, F(11, 'waiting', { pending: 1, total: 2, submitted_names: ['爱丽丝'], pending_names: ['鲍勃'] }))
    expect(room.waiting.total).toBe(2)

    RS.applyFrame(room, F(12, 'floor_start', { floor: 4, reason: 'player' }))
    expect(room.waiting).toStrictEqual({ pending: 0, total: 0, submittedNames: [], pendingNames: [] })
    expect(room.floor.running).toBe(true)
  })

  test('session 事件：更新 status 并返回 fetchSession，未知 status 不改本地值', () => {
    const room = liveRoom()
    expect(room.status).toBe('playing')
    expect(RS.applyFrame(room, F(11, 'session', { status: 'ended', reason: 'end' }))).toStrictEqual(['fetchSession'])
    expect(room.status).toBe('ended')
    expect(RS.applyFrame(room, F(12, 'session', { status: 'lobby', reason: 'cards' }))).toStrictEqual(['fetchSession'])
    expect(room.status).toBe('lobby')
    expect(RS.applyFrame(room, F(13, 'session', { status: 'bogus', reason: 'join' }))).toStrictEqual(['fetchSession'])
    expect(room.status).toBe('lobby')
  })

  test('floor_end(context) 紧跟 floor_start(resume)，最终 running=true 且编号更新', () => {
    const room = liveRoom({ isHost: true })
    feed(room, F(11, 'floor_start', { floor: 3, reason: 'player' }))
    room.floor.starting = true
    feed(room, F(0, 'kp_status', { text: 'KP 正在整理记忆' }))
    expect(room.kpStatus).toBe('KP 正在整理记忆')

    feed(room, F(12, 'floor_end', { floor: 3, reason: 'context', messages: 2 }))
    expect(room.floor.running).toBe(false)
    expect(room.kpStatus).toBe('')
    feed(room, F(13, 'floor_start', { floor: 4, reason: 'resume' }))
    expect(room.floor.running).toBe(true)
    expect(room.floor.starting).toBe(false)
    expect(room.floor.number).toBe(4)
    expect(RS.gates(room).canStop).toBe(true)
    expect(RS.gates(room).canContinue).toBe(false)
  })

  test('floor_end 遇到未知 reason 也按周期已结束处理', () => {
    const room = liveRoom()
    feed(room, F(11, 'floor_start', { floor: 1, reason: 'opening' }))
    feed(room, F(12, 'floor_end', { floor: 1, reason: 'something_new', messages: 0 }))
    expect(room.floor.running).toBe(false)
  })

  test('kp_status 设置提示文案', () => {
    const room = liveRoom()
    RS.applyFrame(room, F(0, 'kp_status', { text: 'KP 正在思考…' }))
    expect(room.kpStatus).toBe('KP 正在思考…')
  })

  test('未知事件被忽略：不报错、不改状态、无副作用', () => {
    const room = liveRoom()
    const before = JSON.parse(JSON.stringify(room))
    expect(RS.applyFrame(room, F(11, 'future_event', { x: 1 }))).toStrictEqual([])
    expect(JSON.parse(JSON.stringify(room))).toStrictEqual(before)
    expect(RS.applyFrame(room, null)).toStrictEqual([])
    expect(RS.applyFrame(room, { seq: 0 })).toStrictEqual([])
    expect(feed(room, F(11, 'future_event')).verdict).toBe('apply')
    expect(room.lastSeq).toBe(11)
  })

  test('applySnapshot：整体替换 messages / spotlight / waiting / floor / submitted / status', () => {
    const room = liveRoom({ userId: 3 })
    feed(room, F(11, 'message_new', { message: msg(1) }))
    RS.markDisconnected(room)
    RS.resetCursor(room)
    RS.onHello(room, { epoch: 'e1', seq: 30 })

    RS.applySnapshot(room, {
      session: { status: 'ended' },
      messages: [msg(9), msg(5), writer(7), msg(5, { content: '重复项' })],
      chatStatus: {
        kp_running: false,
        submitted: true,
        floor: { floor: 6, started_at: '2026-11-10T06:00:00Z', beats: 2 },
        spotlight: { user_ids: [4], names: ['鲍勃'], question: '去哪', kp_floor: false, asked_me: true, stop: '' },
        waiting: { pending: 1, total: 2, submitted_names: ['爱丽丝'], pending_names: ['鲍勃'] },
      },
    })
    expect(room.status).toBe('ended')
    expect(room.messages.map((m) => m.id)).toStrictEqual([5, 7, 9])
    expect(room.byId[1]).toBeUndefined()
    expect(room.floor).toStrictEqual({ running: true, number: 6, starting: false, startedAt: Date.parse('2026-11-10T06:00:00Z') })
    expect(room.me.submitted).toBe(true)
    expect(room.spotlight.askedMe).toBe(true)
    expect(room.spotlight.userIds).toStrictEqual([4])
    expect(room.waiting).toStrictEqual({ pending: 1, total: 2, submittedNames: ['爱丽丝'], pendingNames: ['鲍勃'] })
    expect(Object.keys(room.streams)).toStrictEqual([])
    expect(room.lastSeq).toBe(30)
  })

  test('applySnapshot：仅 kp_running 为 true 也算周期运行中；askedMe 可由 userIds 推出', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 3 })
    RS.onHello(room, { epoch: 'e1', seq: 1 })
    RS.applySnapshot(
      room,
      snap({
        chatStatus: {
          kp_running: true,
          floor: null,
          spotlight: { user_ids: [3], names: ['我'], question: '', kp_floor: false, asked_me: false, stop: '' },
        },
      }),
    )
    expect(room.floor.running).toBe(true)
    expect(room.floor.number).toBe(0)
    expect(room.spotlight.askedMe).toBe(true)
    expect(room.me.submitted).toBe(false)
  })

  test('applySnapshot：缺省的 chatStatus 字段按空值处理，不抛错', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 3 })
    RS.onHello(room, { epoch: 'e1', seq: 1 })
    RS.applySnapshot(room, { session: { status: 'lobby' }, messages: null, chatStatus: {} })
    expect(room.status).toBe('lobby')
    expect(room.messages).toStrictEqual([])
    expect(room.floor).toStrictEqual({ running: false, number: 0, starting: false, startedAt: 0 })
    expect(room.spotlight.userIds).toStrictEqual([])
  })

  test('applySnapshot：拉快照期间连接已断时数据照常应用，但不把 conn 写回 live', () => {
    const room = RS.newRoom({ sessionId: 1, userId: 3 })
    RS.onHello(room, { epoch: 'e1', seq: 5 })
    RS.markDisconnected(room)
    RS.applySnapshot(room, snap({ messages: [msg(1)] }))
    expect(room.conn).toBe('reconnecting')
    expect(room.messages.length).toBe(1)
    expect(room.lastSeq).toBe(5)
    expect(RS.gates(room).live).toBe(false)
    expect(RS.onHello(room, { epoch: 'e1', seq: 7 })).toStrictEqual([])
  })

  test('markDisconnected：保留流式正文，kpStatus 清空，conn=reconnecting', () => {
    const room = liveRoom()
    feed(room, F(11, 'message_new', { message: writer(1) }))
    feed(room, F(12, 'message_new', { message: writer(2) }))
    feed(room, F(0, 'writer_token', { message_id: 1, text: 'a' }))
    feed(room, F(0, 'kp_status', { text: '思考中' }))
    RS.markDisconnected(room)
    expect(room.conn).toBe('reconnecting')
    expect(room.kpStatus).toBe('')
    expect(room.streams[1]).toStrictEqual({ text: 'a' })
    expect(room.streams[2]).toBeUndefined()
    expect(room.lastSeq).toBe(12)
  })

  test('所有函数原地修改：嵌套对象引用不变', () => {
    const room = liveRoom()
    const keys = ['messages', 'byId', 'streams', 'floor', 'spotlight', 'waiting', 'me', 'pending'] as const
    const refs = keys.map((k) => room[k])

    feed(room, F(11, 'message_new', { message: writer(1) }))
    RS.markDisconnected(room)
    RS.onHello(room, { epoch: 'e2', seq: 3 })
    RS.acceptFrame(room, F(4, 'floor_start', { floor: 1 }))
    RS.applySnapshot(
      room,
      snap({
        messages: [msg(2)],
        chatStatus: {
          spotlight: { user_ids: [3], names: ['我'], question: '', kp_floor: false, asked_me: true, stop: '' },
        },
      }),
    )
    RS.resetCursor(room)

    keys.forEach((k, i) => expect(room[k], k).toBe(refs[i]))
  })

  // NOTE: 对应旧版"模拟 Alpine 深层代理"：Alpine 的响应式底层就是 @vue/reactivity，这里直接用 Vue 的 reactive 验证
  test('在 Vue reactive 代理上调用，状态写进原始对象并触发 watchEffect', () => {
    const target = RS.newRoom({ sessionId: 1, userId: 3, isHost: true })
    const room = reactive(target) as Room

    let seenConn = ''
    let connRuns = 0
    let seenText: string | undefined
    let textRuns = 0
    const stopConn = watchEffect(
      () => {
        seenConn = room.conn
        connRuns++
      },
      { flush: 'sync' },
    )
    const stopText = watchEffect(
      () => {
        seenText = room.streams[2]?.text
        textRuns++
      },
      { flush: 'sync' },
    )
    const connRunsBefore = connRuns

    RS.onHello(room, { epoch: 'e1', seq: 5 })
    RS.acceptFrame(room, F(6, 'floor_start', { floor: 1 }))
    RS.applySnapshot(room, snap({ messages: [writer(1)] }))
    feed(room, F(7, 'message_new', { message: writer(2) }))
    feed(room, F(0, 'writer_token', { message_id: 2, text: '雨' }))
    stopConn()
    stopText()

    expect(target.conn).toBe('live')
    expect(target.lastSeq).toBe(7)
    expect(target.floor.running).toBe(true)
    expect(target.messages.map((m) => m.id)).toStrictEqual([1, 2])
    expect(target.streams[2].text).toBe('雨')

    expect(connRuns).toBeGreaterThan(connRunsBefore)
    expect(seenConn).toBe('live')
    expect(textRuns).toBeGreaterThan(1)
    expect(seenText).toBe('雨')
  })

  test('gates 真值表', () => {
    const base = (): Room => {
      const r = RS.newRoom({ sessionId: 1, userId: 3, isHost: true })
      r.conn = 'live'
      r.status = 'playing'
      r.spotlight.userIds = [3]
      r.spotlight.askedMe = true
      return r
    }
    const cases: Array<[string, (r: Room) => void, Gates]> = [
      ['被点名，周期空闲', () => {}, { canSpeak: true, canContinue: true, canStop: false, live: true }],
      [
        '未被点名',
        (r) => {
          r.spotlight.userIds = [4]
          r.spotlight.askedMe = false
        },
        { canSpeak: false, canContinue: true, canStop: false, live: true },
      ],
      [
        '周期运行中',
        (r) => {
          r.floor.running = true
        },
        { canSpeak: false, canContinue: false, canStop: true, live: true },
      ],
      [
        'spotlight.stop 非空（userIds 为空）',
        (r) => {
          r.spotlight.userIds = []
          r.spotlight.askedMe = false
          r.spotlight.stop = 'host_stop'
        },
        { canSpeak: false, canContinue: true, canStop: false, live: true },
      ],
      [
        '点击继续后等待 floor_start',
        (r) => {
          r.floor.starting = true
        },
        { canSpeak: true, canContinue: false, canStop: false, live: true },
      ],
      [
        'conn=helloPending 视为 live',
        (r) => {
          r.conn = 'helloPending'
        },
        { canSpeak: true, canContinue: true, canStop: false, live: true },
      ],
      [
        'conn=snapshot',
        (r) => {
          r.conn = 'snapshot'
        },
        { canSpeak: false, canContinue: false, canStop: false, live: false },
      ],
      [
        'conn=reconnecting 且周期运行中',
        (r) => {
          r.conn = 'reconnecting'
          r.floor.running = true
        },
        { canSpeak: false, canContinue: false, canStop: false, live: false },
      ],
      [
        'conn=connecting',
        (r) => {
          r.conn = 'connecting'
        },
        { canSpeak: false, canContinue: false, canStop: false, live: false },
      ],
      [
        'status=ended',
        (r) => {
          r.status = 'ended'
        },
        { canSpeak: false, canContinue: false, canStop: false, live: true },
      ],
      [
        'status=lobby',
        (r) => {
          r.status = 'lobby'
        },
        { canSpeak: false, canContinue: false, canStop: false, live: true },
      ],
      [
        '非房主，被点名',
        (r) => {
          r.isHost = false
        },
        { canSpeak: true, canContinue: false, canStop: false, live: true },
      ],
      [
        '非房主，周期运行中',
        (r) => {
          r.isHost = false
          r.floor.running = true
        },
        { canSpeak: false, canContinue: false, canStop: false, live: true },
      ],
    ]
    for (const [name, mutate, want] of cases) {
      const room = base()
      mutate(room)
      expect(RS.gates(room), name).toStrictEqual(want)
    }
  })

  test('parseDice：解析多段、难度、暗骰，匹配不上的段保留 raw', () => {
    const input = '侦查鉴定: 45 ; 聆听鉴定: 12 难度:困难 ; 潜行鉴定: 80 难度:极难  (暗骰); 乱七八糟; '
    expect(RS.parseDice(input)).toStrictEqual([
      { what: '侦查', roll: '45', level: '', hidden: false },
      { what: '聆听', roll: '12', level: '困难', hidden: false },
      { what: '潜行', roll: '80', level: '极难', hidden: true },
      { raw: '乱七八糟' },
    ])
  })

  test('parseDice：只有暗骰没有难度，兼容全角冒号', () => {
    expect(RS.parseDice('图书馆使用鉴定: 33  (暗骰)')).toStrictEqual([
      { what: '图书馆使用', roll: '33', level: '', hidden: true },
    ])
    expect(RS.parseDice('力量鉴定：50 难度：普通')).toStrictEqual([
      { what: '力量', roll: '50', level: '普通', hidden: false },
    ])
  })

  test('parseDice：空输入返回 []', () => {
    expect(RS.parseDice('')).toStrictEqual([])
    expect(RS.parseDice(undefined)).toStrictEqual([])
    expect(RS.parseDice(null)).toStrictEqual([])
    expect(RS.parseDice('; ;')).toStrictEqual([])
  })

  test('parseTimePoint：拆出天数、时刻和已过时间', () => {
    expect(RS.parseTimePoint('第2天 08:30(距开局已过16h30m)')).toStrictEqual({
      raw: '第2天 08:30(距开局已过16h30m)',
      label: '第2天 08:30',
      elapsed: '距开局已过16h30m',
    })
    expect(RS.parseTimePoint('第1天 8:00（距开局已过0m）')).toStrictEqual({
      raw: '第1天 8:00（距开局已过0m）',
      label: '第1天 8:00',
      elapsed: '距开局已过0m',
    })
    expect(RS.parseTimePoint('第3天 21:00')).toStrictEqual({ raw: '第3天 21:00', label: '第3天 21:00', elapsed: '' })
  })

  test('parseTimePoint：RFC 本地时间转成年月日，没有已过时间', () => {
    expect(RS.parseTimePoint('1924-09-03T21:30')).toStrictEqual({
      raw: '1924-09-03T21:30',
      label: '1924年9月3日 21:30',
      elapsed: '',
    })
    expect(RS.parseTimePoint('1924-12-25T08:05')?.label).toBe('1924年12月25日 08:05')
    expect(RS.parseTimePoint('  1924-01-01T00:00  ')).toStrictEqual({
      raw: '1924-01-01T00:00',
      label: '1924年1月1日 00:00',
      elapsed: '',
    })
  })

  test('parseTimePoint：RFC 容忍空格分隔，带秒时只取到分', () => {
    expect(RS.parseTimePoint('1924-09-03 21:30')?.label).toBe('1924年9月3日 21:30')
    expect(RS.parseTimePoint('1924-09-03T21:30:45')).toStrictEqual({
      raw: '1924-09-03T21:30:45',
      label: '1924年9月3日 21:30',
      elapsed: '',
    })
  })

  test('parseTimePoint：格式不符保留原文，空输入返回 null', () => {
    expect(RS.parseTimePoint('黄昏时分')).toStrictEqual({ raw: '黄昏时分', label: '黄昏时分', elapsed: '' })
    expect(RS.parseTimePoint('')).toBeNull()
    expect(RS.parseTimePoint('   ')).toBeNull()
    expect(RS.parseTimePoint(undefined)).toBeNull()
    expect(RS.parseTimePoint(null)).toBeNull()
  })

  test('模块导出的 API 清单', () => {
    expect(Object.keys(RS).sort()).toStrictEqual([
      'acceptFrame',
      'applyChatStatus',
      'applyFrame',
      'applySnapshot',
      'gates',
      'markDisconnected',
      'newRoom',
      'onHello',
      'parseDice',
      'parseTimePoint',
      'resetCursor',
    ])
  })
})
