// @vitest-environment jsdom
// NOTE: 用假的 WebSocket 和 vitest 假定时器驱动 RoomController 的连接、命令和门控；问题卡、横幅的纯逻辑断言分别在 ask.test.ts / banner.test.ts
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { askLocked, askLockNote, askView, createAskDraft, draftAnswer } from './ask'
import { roomBanner, continueLabel } from './banner'
import { createRoomController } from './roomController'
import type { RoomController, RoomDeps } from './roomController'
import type { Frame, Room } from './roomState'
import { gates } from './roomState'
import { writerText } from './messages'

class FakeWS {
  onmessage: ((ev: MessageEvent) => void) | null = null
  onclose: (() => void) | null = null
  closed = false
  // NOTE: 与真实 WebSocket 一致：0 建立中，3 已关闭；控制器靠它判断唤醒时要不要重连
  readyState = 0
  constructor(
    readonly url: string,
    sockets: FakeWS[],
  ) {
    sockets.push(this)
  }
  close() {
    this.closed = true
    this.readyState = 3
  }
  recv(frame: Frame) {
    this.onmessage?.({ data: JSON.stringify(frame) } as MessageEvent)
  }
  drop() {
    this.readyState = 3
    this.onclose?.()
  }
}

const SPOT_ME = { user_ids: [2], names: ['爱丽丝'], question: '怎么办？', kp_floor: false, asked_me: true, stop: '' }
const WAITING_ME = { pending: 1, total: 1, submitted_names: [], pending_names: ['爱丽丝'] }
const SPOT_KP = { user_ids: [], names: [], question: '', kp_floor: true, asked_me: false, stop: '' }

const flush = async () => {
  for (let i = 0; i < 20; i++) await Promise.resolve()
}

interface HarnessOptions {
  isHost?: boolean
  spotlight?: Record<string, unknown>
  waiting?: Record<string, unknown>
  messages?: unknown[]
}

function harness({ isHost = false, spotlight = SPOT_ME, waiting = WAITING_ME, messages = [] }: HarnessOptions = {}) {
  const sockets: FakeWS[] = []
  const calls: string[] = []
  const toasts: Array<[string, string | undefined]> = []
  const posted: Array<{ path: string; content: string }> = []
  const frames: Frame[] = []
  const state = {
    postStatus: 202,
    postBody: {} as unknown,
    postThrows: false,
    override: null as null | ((method: string, path: string) => Promise<unknown> | undefined),
    confirmResult: true,
  }
  const session = {
    id: 7,
    status: 'playing',
    created_by: isHost ? 2 : 1,
    players: [{ user_id: 2, character_card: { name: '爱丽丝', stats: { hp: 10, san: 50 } } }],
  }

  const deps: RoomDeps = {
    api: (async (method: string, path: string) => {
      calls.push(method + ' ' + path)
      if (state.override) {
        const r = state.override(method, path)
        if (r !== undefined) return r
      }
      if (path === '/api/sessions/7') return structuredClone(session)
      if (path === '/api/sessions/7/messages') return structuredClone(messages)
      if (path === '/api/sessions/7/chat-status') {
        return { kp_running: false, submitted: false, floor: null, spotlight: structuredClone(spotlight), waiting: structuredClone(waiting) }
      }
      return {}
    }) as RoomDeps['api'],
    postForm: async (path, form) => {
      if (state.postThrows) throw new TypeError('network')
      posted.push({ path, content: String(form.get('content')) })
      return { status: state.postStatus, data: state.postBody }
    },
    token: () => 'tok en',
    user: () => ({ id: 2, role: 'user' }),
    toast: (m, t) => toasts.push([m, t]),
    confirm: async () => state.confirmResult,
    onAuthLost: vi.fn(),
    onRoomGone: vi.fn(),
    onFrame: (f) => frames.push(f),
    createSocket: (url) => new FakeWS(url, sockets) as unknown as WebSocket,
  }
  const ctl: RoomController = createRoomController(deps)

  const h = {
    ctl,
    deps,
    state,
    sockets,
    calls,
    toasts,
    posted,
    frames,
    room: () => ctl.room.value as Room,
    ws: () => sockets[sockets.length - 1],
    // 进房并完成 hello -> 快照，使房间进入 live
    async enter({ epoch = 'E1', seq = 3 } = {}) {
      await ctl.enter(7)
      h.ws().recv({ seq: 0, type: 'hello', data: { epoch, seq } })
      await flush()
    },
    frame(seq: number, type: string, data: Record<string, unknown> = {}) {
      h.ws().recv({ seq, type, data })
    },
    // 触发退避定时器并建立下一条连接
    reconnect(ms = 1000) {
      vi.advanceTimersByTime(ms)
    },
  }
  return h
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('RoomController', () => {
  test('进房：URL 带 token，首次不带 since/epoch；hello 之前不能发言', async () => {
    const h = harness()
    await h.ctl.enter(7)
    expect(h.sockets.length).toBe(1)
    expect(h.ws().url).toBe(`ws://${location.host}/api/sessions/7/ws?token=tok%20en`)
    expect(h.room().conn).toBe('connecting')
    expect(gates(h.room()).canSpeak).toBe(false)
    expect(askView(h.room(), createAskDraft())).toBe('')
  })

  test('进房：快照到达前没有问题卡，之后显示并带上提问', async () => {
    const h = harness({ messages: [{ id: 1, kind: 'kp', status: 'done', floor: 1, content: '开场', role: 'assistant' }] })
    const draft = createAskDraft()
    await h.ctl.enter(7)
    expect(askView(h.room(), draft)).toBe('')
    h.ws().recv({ seq: 0, type: 'hello', data: { epoch: 'E1', seq: 3 } })
    await flush()
    const room = h.room()
    expect(room.conn).toBe('live')
    expect(room.messages.length).toBe(1)
    expect(gates(room).canSpeak).toBe(true)
    expect(askView(room, draft)).toBe('card')
    expect(askLocked(room)).toBe(false)
    expect(room.spotlight.question).toBe('怎么办？')
    expect(roomBanner(room)).toStrictEqual({ text: 'KP 在等你回应', tone: 'me', busy: false })
    expect(gates(room).canContinue).toBe(false)
    expect(h.calls.filter((c) => c.includes('/api/sessions/7')).sort()).toStrictEqual([
      'GET /api/sessions/7',
      'GET /api/sessions/7',
      'GET /api/sessions/7/chat-status',
      'GET /api/sessions/7/messages',
    ])
    expect(h.ctl.session.value?.id).toBe(7)
  })

  test('补发的旧帧被丢弃；周期运行中没有问题卡；writer 流式文字', async () => {
    const h = harness()
    await h.enter()
    const draft = createAskDraft()
    h.frame(3, 'message_new', { message: { id: 9, kind: 'kp', status: 'done', content: '旧' } })
    expect(h.room().byId[9]).toBeUndefined()

    h.frame(4, 'floor_start', { floor: 1, reason: 'player' })
    expect(gates(h.room()).canSpeak).toBe(false)
    expect(askView(h.room(), draft)).toBe('')
    expect(roomBanner(h.room())).toStrictEqual({ text: 'KP 正在推进剧情…', tone: 'info', busy: true })

    h.frame(5, 'message_new', { message: { id: 10, kind: 'writer', status: 'pending', floor: 1, content: '' } })
    expect(h.room().byId[10]._new).toBe(true)
    h.frame(0, 'writer_token', { message_id: 10, text: '雨' })
    h.frame(0, 'writer_token', { message_id: 10, text: '落下' })
    expect(writerText(h.room(), h.room().byId[10])).toBe('雨落下')
    h.frame(6, 'message_update', { id: 10, status: 'done', content: '雨落下了。' })
    expect(writerText(h.room(), h.room().byId[10])).toBe('雨落下了。')

    h.frame(7, 'floor_end', { floor: 1, reason: 'ask', messages: 1 })
    h.frame(8, 'spotlight', { user_ids: [2], names: ['爱丽丝'], question: '下一步？', kp_floor: false, stop: '' })
    expect(askView(h.room(), draft)).toBe('card')
    expect(h.room().spotlight.question).toBe('下一步？')
  })

  test('提交：202 后记下已提交内容，不插乐观消息；表单字段是 content', async () => {
    const h = harness()
    await h.enter()
    expect(await h.ctl.sendChat('')).toBe(false)
    expect(h.posted.length).toBe(0)

    expect(await h.ctl.sendChat('敲门\n我推门')).toBe(true)
    expect(h.posted).toStrictEqual([{ path: '/api/sessions/7/chat', content: '敲门\n我推门' }])
    expect(h.room().me.submitted).toBe(true)
    expect(h.room().me.draft).toBe('敲门\n我推门')
    expect(h.room().messages.length).toBe(0)
    expect(askView(h.room(), createAskDraft())).toBe('done')
  })

  test('提交：按字符数限制 2000，被拒绝时 toast 并按状态码处理', async () => {
    const h = harness()
    await h.enter()

    expect(await h.ctl.sendChat('平'.repeat(2001))).toBe(false)
    expect(h.toasts.at(-1)?.[0]).toMatch(/消息过长/)
    expect(h.posted.length).toBe(0)

    expect(await h.ctl.sendChat('平'.repeat(2000))).toBe(true)
    h.room().me.submitted = false

    const before = h.calls.filter((c) => c.endsWith('/chat-status')).length
    h.state.postStatus = 409
    h.state.postBody = { error: 'KP 正在推进剧情' }
    expect(await h.ctl.sendChat('x')).toBe(false)
    expect(h.toasts.at(-1)?.[0]).toBe('KP 正在推进剧情')
    expect(h.room().me.submitted).toBe(false)
    await flush()
    expect(h.calls.filter((c) => c.endsWith('/chat-status')).length).toBe(before + 1)

    h.state.postStatus = 401
    h.state.postBody = {}
    expect(await h.ctl.sendChat('x')).toBe(false)
    expect(h.toasts.at(-1)?.[0]).toBe('发送失败')
    expect(h.deps.onAuthLost).toHaveBeenCalledTimes(1)

    h.state.postThrows = true
    expect(await h.ctl.sendChat('x')).toBe(false)
    expect(h.toasts.at(-1)?.[0]).toBe('网络错误，请重试')
  })

  test('断线：卡片和已写的内容保留且不能提交；同一 epoch 重连成功后立即解锁', async () => {
    const h = harness()
    await h.enter()
    const draft = createAskDraft()
    h.room().messages.push({ id: 200, kind: 'kp', status: 'done', meta: { options: ['敲门'] } })
    draft.input = '我先写着'
    draft.pick = ['敲门']

    h.ws().drop()
    const room = h.room()
    expect(room.conn).toBe('reconnecting')
    expect(askView(room, draft)).toBe('card')
    expect(askLocked(room)).toBe(true)
    expect(askLockNote(room)).toMatch(/连接中断/)
    expect(draftAnswer(room, draft)).toBe('敲门\n我先写着')
    expect(await h.ctl.sendChat(draftAnswer(room, draft))).toBe(false)
    expect(h.posted.length).toBe(0)
    expect(roomBanner(room).text).toBe('连接中断，正在重连…')
    expect(roomBanner(room).tone).toBe('warn')

    expect(h.sockets.length).toBe(1)
    h.reconnect(999)
    expect(h.sockets.length).toBe(1)
    h.reconnect(1)
    expect(h.sockets.length).toBe(2)
    expect(h.ws().url).toMatch(/&since=3&epoch=E1$/)
    h.ws().recv({ seq: 0, type: 'hello', data: { epoch: 'E1', seq: 3 } })
    expect(askLocked(room)).toBe(false)
    expect(await h.ctl.sendChat(draftAnswer(room, draft))).toBe(true)
    expect(h.posted.at(-1)?.content).toBe('敲门\n我先写着')
  })

  test('断线后服务重启（epoch 变化）：同步快照期间置灰，快照后解锁', async () => {
    const h = harness()
    await h.enter()
    h.ws().drop()
    h.reconnect()

    h.ws().recv({ seq: 0, type: 'hello', data: { epoch: 'E2', seq: 0 } })
    const room = h.room()
    expect(room.conn).toBe('snapshot')
    expect(askLocked(room)).toBe(true)
    expect(askLockNote(room)).toMatch(/同步/)
    await flush()
    expect(room.epoch).toBe('E2')
    expect(room.conn).toBe('live')
    expect(askLocked(room)).toBe(false)
  })

  test('重连：断线期间错过的内容被补齐；旧连接的迟到帧被忽略', async () => {
    const h = harness()
    await h.enter()
    h.frame(4, 'message_new', { message: { id: 11, kind: 'writer', status: 'pending', floor: 2, content: '' } })
    h.frame(0, 'writer_token', { message_id: 11, text: '半截' })

    const first = h.ws()
    first.drop()
    expect(writerText(h.room(), h.room().byId[11])).toBe('半截')
    h.reconnect()
    expect(h.ws().url).toMatch(/&since=4&epoch=E1$/)
    h.ws().recv({ seq: 0, type: 'hello', data: { epoch: 'E1', seq: 7 } })
    h.frame(5, 'some_future_event', {})
    h.frame(6, 'message_update', { id: 11, status: 'done', content: '完整正文' })
    h.frame(7, 'floor_end', { floor: 2, reason: 'ask', messages: 1 })
    expect(h.room().conn).toBe('live')
    expect(writerText(h.room(), h.room().byId[11])).toBe('完整正文')

    const n = h.room().messages.length
    first.recv({ seq: 8, type: 'message_new', data: { message: { id: 99, kind: 'kp', status: 'done', content: 'x' } } })
    expect(h.room().messages.length).toBe(n)
  })

  test('序号出现缺口：丢弃游标并重连，不带 since', async () => {
    const h = harness()
    await h.enter()
    h.frame(4, 'floor_end', { floor: 1, reason: 'ask', messages: 0 })
    h.frame(9, 'message_new', { message: { id: 50, kind: 'kp', status: 'done', content: 'gap' } })
    expect(h.sockets.length).toBe(2)
    expect(h.ws().url).not.toMatch(/since=/)
    expect(h.ws().url).toMatch(/epoch=E1/)
  })

  test('页面重新可见或网络恢复时不等退避，立即重连', async () => {
    const h = harness()
    await h.enter()
    h.ws().drop()
    expect(h.sockets.length).toBe(1)
    window.dispatchEvent(new Event('online'))
    expect(h.sockets.length).toBe(2)

    h.ws().drop()
    document.dispatchEvent(new Event('visibilitychange'))
    expect(h.sockets.length).toBe(3)

    // 连接还在建立时不重复建立
    document.dispatchEvent(new Event('visibilitychange'))
    expect(h.sockets.length).toBe(3)
  })

  test('chat-status 响应期间又有事件到达：丢弃过期响应并重拉，最多重拉 2 次', async () => {
    const h = harness()
    await h.enter()
    const pending: Array<(v: unknown) => void> = []
    h.state.override = (_m, p) => (p.endsWith('/chat-status') ? new Promise((res) => pending.push(res)) : undefined)
    const submitted = { submitted: true, spotlight: SPOT_ME, waiting: WAITING_ME }

    h.frame(4, 'waiting', WAITING_ME)
    expect(pending.length).toBe(1)
    h.frame(5, 'kp_status', {})
    pending[0](submitted)
    await flush()
    expect(pending.length).toBe(2)
    expect(h.room().me.submitted).toBe(false)

    h.frame(6, 'kp_status', {})
    pending[1](submitted)
    await flush()
    expect(pending.length).toBe(3)

    h.frame(7, 'kp_status', {})
    pending[2](submitted)
    await flush()
    expect(pending.length).toBe(3)
    expect(h.room().me.submitted).toBe(false)
  })

  test('没等到 hello 就断开：首次连接一次失败就探测，探测到房间不存在则离开', async () => {
    const h = harness()
    await h.ctl.enter(7)
    h.state.override = (_m, p) =>
      p.endsWith('/messages') ? Promise.reject(new ApiError('房间不存在', 404)) : undefined
    h.ws().drop()
    await flush()
    expect(h.calls).toContain('GET /api/auth/me')
    expect(h.toasts.at(-1)?.[0]).toBe('房间不存在')
    expect(h.deps.onRoomGone).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  test('没等到 hello 就断开：登录失效时通知调用方', async () => {
    const h = harness()
    await h.ctl.enter(7)
    h.state.override = (_m, p) => (p === '/api/auth/me' ? Promise.reject(new ApiError('未登录', 401)) : undefined)
    h.ws().drop()
    await flush()
    expect(h.deps.onAuthLost).toHaveBeenCalledTimes(1)
    expect(h.toasts.at(-1)?.[0]).toBe('登录已失效，请重新登录')
  })

  test('已有 epoch 的重连场景：连续失败 3 次才探测，探测通过后继续退避重连', async () => {
    const h = harness()
    await h.enter()
    h.ws().drop()
    h.reconnect(1000)
    h.ws().drop()
    h.reconnect(2000)
    h.ws().drop()
    h.reconnect(4000)
    expect(h.calls).not.toContain('GET /api/auth/me')
    expect(h.sockets.length).toBe(4)
    h.ws().drop()
    await flush()
    expect(h.calls).toContain('GET /api/auth/me')
    expect(h.deps.onRoomGone).not.toHaveBeenCalled()
    h.reconnect(8000)
    expect(h.sockets.length).toBe(5)
  })

  test('快照拉取返回 403/404：提示并通知调用方离开房间', async () => {
    const h = harness()
    await h.ctl.enter(7)
    h.state.override = (_m, p) =>
      p.endsWith('/messages') ? Promise.reject(new ApiError('无权查看', 403)) : undefined
    h.ws().recv({ seq: 0, type: 'hello', data: { epoch: 'E1', seq: 3 } })
    await flush()
    expect(h.toasts.at(-1)?.[0]).toBe('无权查看')
    expect(h.deps.onRoomGone).toHaveBeenCalledTimes(1)
  })

  test('房主：继续与打断的门控、命令和启动中状态', async () => {
    const h = harness({ isHost: true, spotlight: SPOT_KP, waiting: { pending: 0, total: 0 } })
    await h.enter()
    const room = h.room()
    expect(gates(room).canContinue).toBe(true)
    expect(gates(room).canStop).toBe(false)
    expect(continueLabel(room)).toBe('让 KP 开场')

    h.frame(4, 'floor_start', { floor: 1, reason: 'host_continue' })
    expect(gates(room).canStop).toBe(true)
    expect(gates(room).canContinue).toBe(false)

    expect(await h.ctl.hostStop()).toBe(true)
    expect(h.calls).toContain('POST /api/sessions/7/kp-stop')

    h.frame(5, 'floor_end', { floor: 1, reason: 'host_stop', messages: 0 })
    h.frame(6, 'spotlight', { user_ids: [], names: [], question: '', kp_floor: true, stop: 'host_stop' })
    expect(roomBanner(room).text).toBe('KP 已暂停：房主打断，等待房主让 KP 继续')
    expect(roomBanner(room).busy).toBe(false)
    expect(askView(room, createAskDraft())).toBe('')
    expect(gates(room).canContinue).toBe(true)

    expect(await h.ctl.hostContinue()).toBe(true)
    expect(room.floor.starting).toBe(true)
    expect(gates(room).canContinue).toBe(false)
    expect(h.calls).toContain('POST /api/sessions/7/kp-turn')

    // 启动超时兜底：一直没收到 floor_start 时按钮不能永远处于启动中
    const before = h.calls.filter((c) => c.endsWith('/chat-status')).length
    vi.advanceTimersByTime(9999)
    expect(room.floor.starting).toBe(true)
    vi.advanceTimersByTime(1)
    expect(room.floor.starting).toBe(false)
    await flush()
    expect(h.calls.filter((c) => c.endsWith('/chat-status')).length).toBe(before + 1)
  })

  test('房主继续：有被点名但未回应的玩家时先确认，取消则不发请求', async () => {
    const h = harness({ isHost: true })
    await h.enter()
    h.room().spotlight.userIds = [3]
    h.state.confirmResult = false
    expect(await h.ctl.hostContinue()).toBe(false)
    expect(h.calls).not.toContain('POST /api/sessions/7/kp-turn')
    expect(h.room().floor.starting).toBe(false)

    h.state.confirmResult = true
    expect(await h.ctl.hostContinue()).toBe(true)
    expect(h.calls).toContain('POST /api/sessions/7/kp-turn')
  })

  test('开场请求被 409 拒绝时静默，并重拉 chat-status；非静默调用才 toast', async () => {
    const h = harness({ isHost: true, spotlight: SPOT_KP, waiting: { pending: 0, total: 0 } })
    await h.enter()
    h.state.override = (m, p) =>
      m === 'POST' && p.endsWith('/kp-turn') ? Promise.reject(new ApiError('已有周期在运行', 409)) : undefined

    const before = h.calls.filter((c) => c.endsWith('/chat-status')).length
    expect(await h.ctl.openScene()).toBe(false)
    expect(h.toasts.length).toBe(0)
    expect(h.room().floor.starting).toBe(false)
    await flush()
    expect(h.calls.filter((c) => c.endsWith('/chat-status')).length).toBe(before + 1)

    expect(await h.ctl.hostContinue()).toBe(false)
    expect(h.toasts.at(-1)?.[0]).toBe('已有周期在运行')
  })

  test('打断被 409 拒绝：提示 KP 没在发言', async () => {
    const h = harness({ isHost: true })
    await h.enter()
    h.frame(4, 'floor_start', { floor: 1, reason: 'host_continue' })
    h.state.override = (m, p) =>
      m === 'POST' && p.endsWith('/kp-stop') ? Promise.reject(new ApiError('冲突', 409)) : undefined
    expect(await h.ctl.hostStop()).toBe(false)
    expect(h.toasts.at(-1)?.[0]).toBe('KP 当前没有在发言')
  })

  test('新周期开始：每个被应用的帧都通知页面，floor_start 供页面清掉问题卡残留', async () => {
    const h = harness()
    await h.enter()
    h.frame(4, 'floor_start', { floor: 2, reason: 'player' })
    h.frame(0, 'kp_status', { text: 'x' })
    expect(h.frames.map((f) => f.type)).toStrictEqual(['floor_start', 'kp_status'])
  })

  test('多人点名：只有被点名的人看到卡片，其余人看状态栏', async () => {
    const h = harness()
    await h.enter()
    const draft = createAskDraft()
    h.frame(4, 'floor_start', { floor: 2, reason: 'player' })
    h.frame(5, 'floor_end', { floor: 2, reason: 'ask', messages: 1 })

    h.frame(6, 'spotlight', { user_ids: [2, 3], names: ['爱丽丝', '鲍勃'], question: '你们呢？', kp_floor: false, stop: '' })
    expect(askView(h.room(), draft)).toBe('card')

    h.frame(7, 'spotlight', { user_ids: [3], names: ['鲍勃'], question: '你呢？', kp_floor: false, stop: '' })
    expect(askView(h.room(), draft)).toBe('')
    expect(roomBanner(h.room()).text).toBe('KP 在等 鲍勃 回应')
    expect(roomBanner(h.room()).busy).toBe(true)
    expect(gates(h.room()).canSpeak).toBe(false)
  })

  test('已提交后的状态栏文案：等待其他人时显示他们的名字', async () => {
    const h = harness({ waiting: { pending: 2, total: 2, submitted_names: [], pending_names: ['爱丽丝', '鲍勃'] } })
    await h.enter()
    await h.ctl.sendChat('我推门')
    h.frame(4, 'waiting', { pending: 1, total: 2, submitted_names: ['爱丽丝'], pending_names: ['鲍勃'] })
    expect(askView(h.room(), createAskDraft())).toBe('done')
    expect(roomBanner(h.room()).text).toBe('已提交行动，还在等 鲍勃')
    expect(roomBanner(h.room()).busy).toBe(true)
  })

  test('房间结束：不能继续，也没有问题卡；重新拉取房间信息有防抖', async () => {
    const h = harness({ isHost: true })
    await h.enter()
    h.frame(4, 'session', { status: 'ended', reason: 'end' })
    h.frame(5, 'session', { status: 'ended', reason: 'end' })
    expect(h.room().status).toBe('ended')
    expect(askView(h.room(), createAskDraft())).toBe('')
    expect(gates(h.room()).canContinue).toBe(false)
    expect(roomBanner(h.room()).text).toBe('游戏已结束')

    const count = () => h.calls.filter((c) => c === 'GET /api/sessions/7').length
    const before = count()
    vi.advanceTimersByTime(299)
    expect(count()).toBe(before)
    vi.advanceTimersByTime(1)
    await flush()
    expect(count()).toBe(before + 1)
  })

  test('refreshSession：房间已被删除时提示并通知调用方离开', async () => {
    const h = harness()
    await h.enter()
    h.state.override = (_m, p) => (p === '/api/sessions/7' ? Promise.reject(new ApiError('不存在', 404)) : undefined)
    await h.ctl.refreshSession(true)
    expect(h.toasts.at(-1)?.[0]).toBe('房间已不存在')
    expect(h.deps.onRoomGone).toHaveBeenCalledTimes(1)
  })

  test('离开房间：关闭连接、清掉定时器和监听；迟到的帧与进房请求都不生效', async () => {
    const h = harness()
    await h.enter()
    const live = h.ws()
    expect(live.closed).toBe(false)
    h.ctl.leave()
    expect(h.ctl.room.value).toBeNull()
    expect(h.ctl.session.value).toBeNull()
    expect(live.closed).toBe(true)
    expect(vi.getTimerCount()).toBe(0)

    const before = h.sockets.length
    window.dispatchEvent(new Event('online'))
    document.dispatchEvent(new Event('visibilitychange'))
    expect(h.sockets.length).toBe(before)

    live.recv({ seq: 5, type: 'floor_start', data: { floor: 9 } })
    expect(h.ctl.room.value).toBeNull()

    const p = h.ctl.enter(7)
    h.ctl.leave()
    await p
    expect(h.sockets.length).toBe(before)
    expect(h.ctl.room.value).toBeNull()
  })

  test('离开房间：断线后排队的重连定时器被取消，不会再建立连接', async () => {
    const h = harness()
    await h.enter()
    h.ws().drop()
    expect(vi.getTimerCount()).toBe(1)
    const before = h.sockets.length
    h.ctl.leave()
    expect(vi.getTimerCount()).toBe(0)
    vi.advanceTimersByTime(60000)
    expect(h.sockets.length).toBe(before)
    expect(h.ctl.room.value).toBeNull()
  })
})
