import { getToken } from './token'
import { ApiError } from './client'

export interface SSEOptions {
  signal?: AbortSignal
}

// NOTE: 合并自旧前端两份手写解析（模组生成、故事编译）。按行解析：event: 记录事件名，data: 每行触发一次回调，空行重置事件名；
// 不拼接多行 data，保持与旧行为一致。握手失败（非 2xx）抛 ApiError，流读取中途出错原样抛出
export async function streamSSE(
  path: string,
  body: unknown,
  onEvent: (event: string, data: string) => void,
  opts: SSEOptions = {},
): Promise<void> {
  const token = getToken()
  const resp = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: 'Bearer ' + token } : {}) },
    body: JSON.stringify(body),
    signal: opts.signal,
  })
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: '请求失败' }))
    throw new ApiError(err.error || 'HTTP ' + resp.status, resp.status)
  }
  if (!resp.body) return

  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  let currentEvent = ''

  const handleLine = (rawLine: string) => {
    const line = rawLine.replace(/\r$/, '')
    if (line.startsWith('event:')) {
      currentEvent = line.slice(6).trim()
    } else if (line.startsWith('data:')) {
      onEvent(currentEvent, line.slice(5).replace(/^ /, ''))
    } else if (line === '') {
      currentEvent = ''
    }
  }

  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    const lines = buf.split('\n')
    buf = lines.pop() ?? ''
    for (const line of lines) handleLine(line)
  }
  if (buf) handleLine(buf)
}
