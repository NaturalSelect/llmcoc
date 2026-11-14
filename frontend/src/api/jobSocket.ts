// NOTE: 模组生成与上传故事编译的 WebSocket 客户端：连接后发一帧 JSON 请求，之后只收服务端的 {type, data} 帧。
// 帧的 type 就是原来 SSE 的事件名，这里转成 onEvent(event, JSON 字符串)，与 scenarioStream.ts 里的事件处理逻辑直接对接。
// 约定：服务端发 rejected 帧表示请求被拒（等同原来的 400），此时 Promise 以 ApiError 失败；
// 任务结束服务端会正常关闭连接，Promise 随之成功；连接中途断开也算成功返回，由调用方根据是否收到终结事件判断。
// 握手失败时浏览器拿不到 HTTP 状态码，Promise 以 ApiError 失败，并顺带探测一次登录态让全局 401 处理函数生效。
import { api, ApiError } from './client'
import { getToken } from './token'

export interface JobSocketOptions {
  signal?: AbortSignal
  // 测试注入点，生产环境走默认实现
  createSocket?: (url: string) => WebSocket
}

function parseFrame(raw: unknown): { type: string; data: unknown } | null {
  if (typeof raw !== 'string') return null
  try {
    const frame = JSON.parse(raw) as { type?: unknown; data?: unknown } | null
    if (!frame || typeof frame.type !== 'string') return null
    return { type: frame.type, data: frame.data }
  } catch {
    return null
  }
}

function rejectionMessage(data: unknown): string {
  const message = (data as { message?: unknown } | null | undefined)?.message
  return typeof message === 'string' && message ? message : '请求被拒绝'
}

export function runJobSocket(
  path: string,
  body: unknown,
  onEvent: (event: string, data: string) => void,
  opts: JobSocketOptions = {},
): Promise<void> {
  const { signal } = opts
  return new Promise<void>((resolve, reject) => {
    if (signal?.aborted) {
      resolve()
      return
    }

    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const url = proto + '://' + location.host + path + '?token=' + encodeURIComponent(getToken())
    let ws: WebSocket
    try {
      ws = opts.createSocket ? opts.createSocket(url) : new WebSocket(url)
    } catch (e) {
      reject(e)
      return
    }

    let opened = false
    let settled = false
    let rejection: ApiError | null = null

    const settle = (err?: unknown) => {
      if (settled) return
      settled = true
      signal?.removeEventListener('abort', onAbort)
      if (err) reject(err)
      else resolve()
    }
    function onAbort() {
      try {
        ws.close(1000)
      } catch {
        // 已关闭的连接再 close 可能抛错，忽略
      }
      settle()
    }
    signal?.addEventListener('abort', onAbort)

    ws.onopen = () => {
      opened = true
      ws.send(JSON.stringify(body))
    }
    ws.onmessage = (ev: MessageEvent) => {
      // NOTE: 中断后连接还要一会儿才真正关闭，期间迟到的帧不能再触发日志和提示
      if (settled) return
      const frame = parseFrame(ev.data)
      if (!frame) return
      if (frame.type === 'rejected') {
        rejection = new ApiError(rejectionMessage(frame.data), 400)
        return
      }
      onEvent(frame.type, JSON.stringify(frame.data ?? null))
    }
    // NOTE: 出错之后一定还会触发 onclose，收尾统一放在 onclose 里
    ws.onerror = () => {}
    ws.onclose = () => {
      if (settled) return
      if (rejection) {
        settle(rejection)
        return
      }
      if (!opened) {
        void api('GET', '/api/auth/me')
          .catch(() => {})
          .then(() => settle(new ApiError('无法连接服务器，登录可能已失效或没有管理员权限', 0)))
        return
      }
      settle()
    }
  })
}
