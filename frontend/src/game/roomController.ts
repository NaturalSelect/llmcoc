// NOTE: 房间 WebSocket 连接、命令与门控。状态归约全部在 roomState.ts（纯逻辑，有单测）；这里只负责连接、命令和副作用调度。
// NOTE: 页面里没有任何轮询定时器：房间进展只来自 WebSocket，断线后靠重连和快照收敛。
// NOTE: 连接句柄和定时器是私有字段，不进响应式状态（WebSocket 被 Proxy 包裹后行为不可靠），所以实例必须用 createRoomController 创建（内部 markRaw），不能放进 reactive/Pinia。
import { markRaw, reactive, shallowRef } from 'vue'
import type { ShallowRef } from 'vue'
import type { HttpMethod } from '@/api/client'
import type { Session } from '@/api/types'
import { CHAT_MAX_CHARS } from './ask'
import * as RS from './roomState'
import type { ChatStatus, Effect, Frame, Room, RoomMessage, RoomStatus } from './roomState'

const BACKOFF = [1000, 2000, 4000, 8000, 15000]
const STARTING_TIMEOUT = 10000
const SESSION_FETCH_DEBOUNCE = 300
const STATUSES: readonly string[] = ['lobby', 'playing', 'ended']
const WS_CONNECTING = 0
const WS_OPEN = 1

type Timer = ReturnType<typeof setTimeout>

export interface RoomDeps {
  api: <T = any>(method: HttpMethod, path: string, body?: unknown) => Promise<T>
  postForm: (path: string, form: FormData) => Promise<{ status: number; data: any }>
  token: () => string
  user: () => { id: number; role: string } | null
  toast: (msg: string, type?: 'error' | 'success') => void
  confirm: (text: string, opts?: { danger?: boolean; confirmText?: string }) => Promise<boolean>
  // 登录失效：调用方负责清 token 并跳转登录页
  onAuthLost: () => void
  // 房间没了或没有权限：调用方负责离开游戏页
  onRoomGone: () => void
  // 每个被应用的帧之后调用，页面据此清问题卡、滚动
  onFrame?: (frame: Frame) => void
  // 快照应用之后调用，页面据此强制滚到底部
  onSnapshot?: () => void
  // 测试注入点
  createSocket?: (url: string) => WebSocket
}

function errStatus(e: unknown): number | undefined {
  return (e as { status?: number } | null)?.status
}

function errMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

export class RoomController {
  readonly room: ShallowRef<Room | null> = shallowRef(null)
  readonly session: ShallowRef<Session | null> = shallowRef(null)

  private readonly deps: RoomDeps
  private ws: WebSocket | null = null
  // 每次建立连接 +1，旧连接的回调和迟到的快照响应据此丢弃
  private gen = 0
  // 每次进入或离开房间 +1，丢弃迟到的进房请求
  private enterGen = 0
  private timer: Timer | null = null
  private backoff = 0
  private failsBeforeHello = 0
  private sessionTimer: Timer | null = null
  private startTimer: Timer | null = null
  private wake: (() => void) | null = null

  constructor(deps: RoomDeps) {
    this.deps = deps
  }

  // ═══════════════════════════════════════════════════════
  // 房间生命周期与连接
  // ═══════════════════════════════════════════════════════
  async enter(id: number): Promise<boolean> {
    this.leave()
    const enterGen = this.enterGen
    let session: Session
    try {
      session = await this.deps.api<Session>('GET', '/api/sessions/' + id)
    } catch (e) {
      this.deps.toast(errMessage(e), 'error')
      return false
    }
    if (enterGen !== this.enterGen) return false

    const user = this.deps.user()
    const isHost = session.created_by === user?.id || user?.role === 'admin'
    // NOTE: 传给 roomState 的必须是 reactive 代理本身，函数原地修改代理才会触发渲染
    const room = reactive(RS.newRoom({ sessionId: session.id, userId: user?.id, isHost })) as Room
    if (STATUSES.includes(session.status)) room.status = session.status as RoomStatus
    this.session.value = session
    this.room.value = room
    this.bindWake()
    this.connect()
    return true
  }

  leave(): void {
    this.enterGen++
    this.gen++
    clearTimeout(this.timer ?? undefined)
    clearTimeout(this.sessionTimer ?? undefined)
    clearTimeout(this.startTimer ?? undefined)
    this.timer = this.sessionTimer = this.startTimer = null
    this.backoff = 0
    this.failsBeforeHello = 0
    this.closeSocket()
    if (this.wake) {
      document.removeEventListener('visibilitychange', this.wake)
      window.removeEventListener('online', this.wake)
      this.wake = null
    }
    this.room.value = null
    this.session.value = null
  }

  private closeSocket(): void {
    if (!this.ws) return
    try {
      this.ws.close(1000)
    } catch {
      // 已关闭的连接再 close 可能抛错，忽略
    }
    this.ws = null
  }

  // NOTE: 页面重新可见或网络恢复时，不等退避定时器，立刻重连
  private bindWake(): void {
    if (this.wake) return
    this.wake = () => {
      if (document.visibilityState === 'hidden') return
      if (!this.room.value) return
      const ws = this.ws
      if (ws && (ws.readyState === WS_OPEN || ws.readyState === WS_CONNECTING)) return
      this.backoff = 0
      this.connect()
    }
    document.addEventListener('visibilitychange', this.wake)
    window.addEventListener('online', this.wake)
  }

  private connect(): void {
    const room = this.room.value
    if (!room) return
    const gen = ++this.gen
    clearTimeout(this.timer ?? undefined)
    this.timer = null
    this.closeSocket()

    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    let url =
      proto + '://' + location.host + '/api/sessions/' + room.sessionId + '/ws?token=' + encodeURIComponent(this.deps.token())
    if (room.lastSeq > 0) url += '&since=' + room.lastSeq
    if (room.epoch) url += '&epoch=' + encodeURIComponent(room.epoch)

    let ws: WebSocket
    try {
      ws = this.deps.createSocket ? this.deps.createSocket(url) : new WebSocket(url)
    } catch {
      this.scheduleReconnect(room)
      return
    }
    this.ws = ws
    let gotHello = false

    ws.onmessage = (ev: MessageEvent) => {
      if (this.gen !== gen || this.room.value !== room) return
      let frame: Frame
      try {
        frame = JSON.parse(ev.data)
      } catch {
        return
      }
      if (!frame || typeof frame.type !== 'string') return

      if (frame.type === 'hello') {
        gotHello = true
        this.backoff = 0
        this.failsBeforeHello = 0
        this.runEffects(room, RS.onHello(room, frame), gen)
        return
      }
      const verdict = RS.acceptFrame(room, frame)
      if (verdict === 'apply') {
        this.runEffects(room, RS.applyFrame(room, frame), gen)
        this.afterFrame(room, frame)
      } else if (verdict === 'resync') {
        this.resync(room)
      }
    }

    ws.onclose = () => {
      if (this.gen !== gen || this.room.value !== room) return
      this.ws = null
      RS.markDisconnected(room)
      // NOTE: 浏览器拿不到握手失败的状态码；没等到 hello 就断开时，主动探测是登录失效、被封禁、没有权限还是房间没了。
      // 首次连接（还没有 epoch）一次失败就探测，重连场景连续三次才探测，避免网络抖动时频繁请求。
      if (!gotHello && ++this.failsBeforeHello >= (room.epoch ? 3 : 1)) {
        this.failsBeforeHello = 0
        void this.probeRoom(room).then((ok) => {
          if (ok && this.room.value === room && this.gen === gen) this.scheduleReconnect(room)
        })
        return
      }
      this.scheduleReconnect(room)
    }
  }

  private scheduleReconnect(room: Room): void {
    if (this.room.value !== room) return
    const delay = BACKOFF[Math.min(this.backoff, BACKOFF.length - 1)]
    this.backoff++
    clearTimeout(this.timer ?? undefined)
    this.timer = setTimeout(() => {
      this.timer = null
      if (this.room.value === room) this.connect()
    }, delay)
  }

  // 本地状态可能缺事件（出现序号缺口、服务端要求 resync）：丢弃游标，立即重连并重新拉快照
  private resync(room: Room): void {
    RS.resetCursor(room)
    this.backoff = 0
    this.connect()
  }

  private async probeRoom(room: Room): Promise<boolean> {
    try {
      await this.deps.api('GET', '/api/auth/me')
    } catch (e) {
      if (this.room.value !== room) return false
      if (errStatus(e) === 401) {
        this.deps.toast('登录已失效，请重新登录', 'error')
        this.deps.onAuthLost()
        return false
      }
    }
    // NOTE: GET /sessions/:id 对所有登录用户开放，有密码房间的权限只在 /messages 和 WebSocket 握手里检查，
    // 所以用 /messages 探测房间是否存在、当前用户是否有权查看。
    try {
      await this.deps.api('GET', '/api/sessions/' + room.sessionId + '/messages')
    } catch (e) {
      if (this.room.value !== room) return false
      const status = errStatus(e)
      if (status === 404 || status === 403) {
        this.deps.toast(errMessage(e), 'error')
        this.deps.onRoomGone()
        return false
      }
    }
    return this.room.value === room
  }

  private runEffects(room: Room, effects: Effect[] | undefined, gen: number): void {
    for (const effect of effects || []) {
      if (effect === 'fetchSnapshot') void this.fetchSnapshot(room, gen)
      else if (effect === 'fetchSession') this.scheduleSessionFetch(room)
      else if (effect === 'fetchChatStatus') void this.fetchChatStatus(room)
    }
  }

  private async fetchSnapshot(room: Room, gen: number): Promise<void> {
    const base = '/api/sessions/' + room.sessionId
    try {
      const [session, messages, chatStatus] = await Promise.all([
        this.deps.api<Session>('GET', base),
        this.deps.api<Array<Partial<RoomMessage>> | null>('GET', base + '/messages'),
        this.deps.api<ChatStatus>('GET', base + '/chat-status'),
      ])
      // NOTE: 拉取期间换了房间或重连过，这份快照已经过期，丢弃；新连接的 hello 会重新触发快照
      if (this.room.value !== room || this.gen !== gen) return
      this.session.value = session
      const effects = RS.applySnapshot(room, { session, messages: messages || [], chatStatus })
      this.runEffects(room, effects, gen)
      this.deps.onSnapshot?.()
    } catch (e) {
      if (this.room.value !== room || this.gen !== gen) return
      const status = errStatus(e)
      if (status === 401) {
        this.deps.toast('登录已失效，请重新登录', 'error')
        this.deps.onAuthLost()
        return
      }
      if (status === 403 || status === 404) {
        this.deps.toast(errMessage(e), 'error')
        this.deps.onRoomGone()
        return
      }
      // 其他错误：关掉连接走重连，重连后的 hello 会重新触发快照
      if (this.ws) {
        try {
          this.ws.close()
        } catch {
          // 已关闭的连接再 close 可能抛错，忽略
        }
      }
    }
  }

  private scheduleSessionFetch(room: Room): void {
    if (this.sessionTimer) return
    this.sessionTimer = setTimeout(() => {
      this.sessionTimer = null
      if (this.room.value === room) void this.refreshSession(true)
    }, SESSION_FETCH_DEBOUNCE)
  }

  async refreshSession(silent = true): Promise<void> {
    const id = this.session.value?.id
    if (!id) return
    try {
      const session = await this.deps.api<Session>('GET', '/api/sessions/' + id)
      if (this.session.value?.id !== id) return
      this.session.value = session
      const room = this.room.value
      if (room && STATUSES.includes(session.status)) room.status = session.status as RoomStatus
    } catch (e) {
      if (!silent) this.deps.toast(errMessage(e), 'error')
      else if (errStatus(e) === 404 && this.room.value && this.session.value?.id === id) {
        this.deps.toast('房间已不存在', 'error')
        this.deps.onRoomGone()
      }
    }
  }

  // NOTE: chat-status 只用来校正发言权、等待进度和"我是否已提交"。请求期间如果又有事件到达，响应可能比事件旧，
  // 直接套用会把刚收到的周期状态盖回去，所以重拉一次而不是应用。
  private async fetchChatStatus(room: Room, attempt = 0): Promise<void> {
    const seq = room.lastSeq
    try {
      const cs = await this.deps.api<ChatStatus>('GET', '/api/sessions/' + room.sessionId + '/chat-status')
      if (this.room.value !== room) return
      if (room.lastSeq !== seq) {
        if (attempt < 2) await this.fetchChatStatus(room, attempt + 1)
        return
      }
      RS.applyChatStatus(room, cs)
    } catch {
      // 校正失败不影响主流程，下一次 waiting 事件会再触发
    }
  }

  private afterFrame(room: Room, frame: Frame): void {
    if (frame.type === 'message_new') {
      const m = room.byId[Number(frame.data?.message?.id)]
      if (m) m._new = true
    }
    this.deps.onFrame?.(frame)
  }

  // ═══════════════════════════════════════════════════════
  // 命令
  // ═══════════════════════════════════════════════════════
  async sendChat(text: string): Promise<boolean> {
    const room = this.room.value
    if (!room) return false
    if (!text || !RS.gates(room).canSpeak) return false
    if ([...text].length > CHAT_MAX_CHARS) {
      this.deps.toast('消息过长（最多 ' + CHAT_MAX_CHARS + ' 字）', 'error')
      return false
    }

    const form = new FormData()
    form.append('content', text)
    let resp: { status: number; data: any }
    try {
      resp = await this.deps.postForm('/api/sessions/' + room.sessionId + '/chat', form)
    } catch {
      this.deps.toast('网络错误，请重试', 'error')
      return false
    }
    if (this.room.value !== room) return false

    if (resp.status === 202) {
      // NOTE: 不插入乐观消息。点名的人到齐、周期开始后，自己的行动才会经 message_new 落进列表。
      room.me.submitted = true
      room.me.draft = text
      return true
    }
    this.deps.toast(resp.data?.error || '发送失败', 'error')
    if (resp.status === 401) this.deps.onAuthLost()
    else if (resp.status === 409 || resp.status === 403) void this.fetchChatStatus(room)
    return false
  }

  // 房主点击「让 KP 继续」：有被点名但未回应的玩家时先确认
  async hostContinue(): Promise<boolean> {
    const room = this.room.value
    if (!room || !RS.gates(room).canContinue) return false
    if (room.spotlight.userIds.length > 0) {
      if (!(await this.deps.confirm('跳过未回应的玩家并让 KP 继续？', { confirmText: '让 KP 继续' }))) return false
      if (this.room.value !== room || !RS.gates(room).canContinue) return false
    }
    return this.postKPTurn(room)
  }

  // NOTE: 新开局的房间由 KP 驱动，开场需要房主主动触发一次 kp-turn（服务端自行判断是否为开场）。
  // 失败不致命：房主仍可通过「让 KP 开场/继续」重试，所以 409 不弹提示。
  async openScene(): Promise<boolean> {
    const room = this.room.value
    if (!room) return false
    return this.postKPTurn(room, { quiet: true })
  }

  private async postKPTurn(room: Room, { quiet = false } = {}): Promise<boolean> {
    room.floor.starting = true
    clearTimeout(this.startTimer ?? undefined)
    // NOTE: 点了继续却一直没收到 floor_start（请求被拒或事件丢了）时，不能让按钮永远处于启动中
    this.startTimer = setTimeout(() => {
      this.startTimer = null
      if (this.room.value === room && room.floor.starting) {
        room.floor.starting = false
        void this.fetchChatStatus(room)
      }
    }, STARTING_TIMEOUT)
    try {
      await this.deps.api('POST', '/api/sessions/' + room.sessionId + '/kp-turn')
      return true
    } catch (e) {
      if (this.room.value !== room) return false
      room.floor.starting = false
      clearTimeout(this.startTimer ?? undefined)
      this.startTimer = null
      const status = errStatus(e)
      if (!(quiet && status === 409)) this.deps.toast(errMessage(e), 'error')
      if (status === 409) void this.fetchChatStatus(room)
      return false
    }
  }

  async hostStop(): Promise<boolean> {
    const room = this.room.value
    if (!room || !RS.gates(room).canStop) return false
    if (!(await this.deps.confirm('打断 KP？本周期已发布的内容会保留。', { danger: true, confirmText: '打断' }))) {
      return false
    }
    if (this.room.value !== room) return false
    try {
      await this.deps.api('POST', '/api/sessions/' + room.sessionId + '/kp-stop')
    } catch (e) {
      if (this.room.value !== room) return false
      if (errStatus(e) === 409) {
        this.deps.toast('KP 当前没有在发言', 'error')
        void this.fetchChatStatus(room)
      } else {
        this.deps.toast(errMessage(e), 'error')
      }
      return false
    }
    // NOTE: 202 之后什么都不做，等 floor_end(host_stop) 推送过来
    return true
  }
}

export function createRoomController(deps: RoomDeps): RoomController {
  return markRaw(new RoomController(deps))
}
