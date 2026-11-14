// NOTE: 线上模组任务进度 WebSocket 的连接控制器：只管连接、退避重连、握手失败探测和帧解析，不依赖 Vue / Pinia。
// 协议是服务端 → 客户端的单向 JSON 帧 {type, data}；快照永远是连接后的第一帧，没有序号、没有补发，重连只靠新快照收敛。
// NOTE: WebSocket 和定时器是私有字段，所以实例不能放进 reactive/Pinia 状态（被 Proxy 包裹后行为不可靠），使用方要自己 markRaw。
import type { OnlineModule, OnlineTaskFrame } from '@/api/adminTypesOnline'
import { api } from '@/api/client'
import type { HttpMethod } from '@/api/client'
import { getToken } from '@/api/token'

const BACKOFF = [1000, 2000, 4000, 8000, 15000]
const WS_CONNECTING = 0
const WS_OPEN = 1

type Timer = ReturnType<typeof setTimeout>

export interface OnlineTaskSocketDeps {
  onSnapshot: (modules: OnlineModule[]) => void
  onModule: (module: OnlineModule) => void
  onDeleted: (id: number) => void
  // NOTE: 只在状态翻转时调用；「已连接」指快照已到达，而不是握手完成
  onStatus: (connected: boolean) => void
  // NOTE: 以下是测试注入点，生产环境走默认实现
  token?: () => string
  api?: (method: HttpMethod, path: string) => Promise<unknown>
  createSocket?: (url: string) => WebSocket
  setTimer?: (fn: () => void, ms: number) => Timer
  clearTimer?: (timer: Timer) => void
}

function isModule(v: unknown): v is OnlineModule {
  return !!v && typeof v === 'object' && typeof (v as { id?: unknown }).id === 'number' && Number.isFinite((v as { id: number }).id)
}

// NOTE: 畸形 JSON、未知 type、字段缺失都返回 null，由调用方直接丢弃；快照里不合法的条目单独滤掉
export function parseOnlineTaskFrame(raw: unknown): OnlineTaskFrame | null {
  if (typeof raw !== 'string') return null
  let frame: unknown
  try {
    frame = JSON.parse(raw)
  } catch {
    return null
  }
  if (!frame || typeof frame !== 'object') return null
  const { type, data } = frame as { type?: unknown; data?: unknown }
  if (type === 'snapshot') {
    const list = (data as { modules?: unknown } | null | undefined)?.modules
    return Array.isArray(list) ? { type, data: { modules: list.filter(isModule) } } : null
  }
  if (type === 'module') return isModule(data) ? { type, data } : null
  if (type === 'deleted') {
    const id = (data as { id?: unknown } | null | undefined)?.id
    return typeof id === 'number' && Number.isFinite(id) ? { type, data: { id } } : null
  }
  return null
}

export class OnlineTaskSocket {
  private readonly deps: OnlineTaskSocketDeps
  private ws: WebSocket | null = null
  // NOTE: 每次建立连接 +1，旧连接迟到的回调和探测结果据此丢弃
  private gen = 0
  private timer: Timer | null = null
  private backoff = 0
  private connected = false
  private running = false
  private wake: (() => void) | null = null

  constructor(deps: OnlineTaskSocketDeps) {
    this.deps = deps
  }

  open(): void {
    if (this.running) return
    this.running = true
    this.backoff = 0
    // NOTE: 网络恢复时不等退避定时器，立刻重连；连接正在建立或已打开时什么都不做
    this.wake = () => {
      if (!this.running) return
      const ws = this.ws
      if (ws && (ws.readyState === WS_OPEN || ws.readyState === WS_CONNECTING)) return
      this.backoff = 0
      this.connect()
    }
    window.addEventListener('online', this.wake)
    this.connect()
  }

  close(): void {
    if (!this.running) return
    this.running = false
    this.gen++
    this.cancelTimer()
    this.closeSocket()
    if (this.wake) {
      window.removeEventListener('online', this.wake)
      this.wake = null
    }
    this.setConnected(false)
  }

  private setConnected(v: boolean): void {
    if (this.connected === v) return
    this.connected = v
    this.deps.onStatus(v)
  }

  private cancelTimer(): void {
    if (this.timer === null) return
    ;(this.deps.clearTimer ?? clearTimeout)(this.timer)
    this.timer = null
  }

  private closeSocket(): void {
    if (!this.ws) return
    try {
      this.ws.close(1000)
    } catch {
      // NOTE: 已关闭的连接再 close 可能抛错，忽略
    }
    this.ws = null
  }

  private connect(): void {
    if (!this.running) return
    const gen = ++this.gen
    this.cancelTimer()
    this.closeSocket()

    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const token = (this.deps.token ?? getToken)()
    const url = proto + '://' + location.host + '/api/admin/online-modules/ws?token=' + encodeURIComponent(token)

    let ws: WebSocket
    try {
      ws = this.deps.createSocket ? this.deps.createSocket(url) : new WebSocket(url)
    } catch {
      this.scheduleReconnect()
      return
    }
    this.ws = ws
    let gotSnapshot = false

    ws.onmessage = (ev: MessageEvent) => {
      if (this.gen !== gen) return
      const frame = parseOnlineTaskFrame(ev.data)
      if (!frame) return
      if (frame.type === 'snapshot') {
        gotSnapshot = true
        this.backoff = 0
        this.deps.onSnapshot(frame.data.modules)
        this.setConnected(true)
      } else if (frame.type === 'module') {
        this.deps.onModule(frame.data)
      } else {
        this.deps.onDeleted(frame.data.id)
      }
    }

    ws.onclose = () => {
      if (this.gen !== gen) return
      this.ws = null
      this.setConnected(false)
      if (gotSnapshot) {
        this.scheduleReconnect()
        return
      }
      // NOTE: 浏览器拿不到握手失败的 HTTP 状态码；没等到快照就断开时，探测一次 /api/auth/me：
      // token 失效会被全局 401 处理函数登出并跳回登录页，其余情况（网络、权限）照常退避重连
      void this.probe().then(() => {
        if (this.running && this.gen === gen) this.scheduleReconnect()
      })
    }
  }

  private async probe(): Promise<void> {
    try {
      await (this.deps.api ?? api)('GET', '/api/auth/me')
    } catch {
      // NOTE: 探测的副作用只在全局 401 处理函数里，这里的错误不需要处理
    }
  }

  private scheduleReconnect(): void {
    if (!this.running) return
    const delay = BACKOFF[Math.min(this.backoff, BACKOFF.length - 1)]
    this.backoff++
    this.cancelTimer()
    this.timer = (this.deps.setTimer ?? setTimeout)(() => {
      this.timer = null
      this.connect()
    }, delay)
  }
}
