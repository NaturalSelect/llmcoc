import { getToken } from './token'

export class ApiError extends Error {
  status: number
  details?: string[]

  constructor(message: string, status: number, details?: string[]) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.details = details
  }
}

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'DELETE'

let unauthorizedHandler: (() => void) | null = null

// NOTE: 登录态模块注册；旧前端 api() 不集中处理 401，调用方逐个判断。这里只对「携带了 token 的请求」触发，
// 登录/注册接口本身返回 401（密码错误）时不会被当成登录失效
export function setUnauthorizedHandler(fn: (() => void) | null): void {
  unauthorizedHandler = fn
}

function authHeader(): Record<string, string> {
  const token = getToken()
  return token ? { Authorization: 'Bearer ' + token } : {}
}

export async function api<T = any>(method: HttpMethod, path: string, body?: unknown): Promise<T> {
  const hadToken = !!getToken()
  const init: RequestInit = {
    method,
    headers: { 'Content-Type': 'application/json', ...authHeader() },
  }
  if (body !== undefined && method !== 'GET') init.body = JSON.stringify(body)
  const resp = await fetch(path, init)
  const data = await resp.json().catch(() => ({}))
  if (!resp.ok) {
    if (resp.status === 401 && hadToken && !path.startsWith('/api/auth/login') && !path.startsWith('/api/auth/register')) {
      unauthorizedHandler?.()
    }
    const details = Array.isArray(data?.details) ? (data.details as string[]) : undefined
    const suffix = details ? '：' + details.join('；') : ''
    throw new ApiError((data?.error || `HTTP ${resp.status}`) + suffix, resp.status, details)
  }
  return data as T
}

// NOTE: /chat 只读表单字段 content，不收 JSON，所以不能用 api()；这里不因 HTTP 状态码抛错，由调用方按状态码分支，只有网络层失败才会抛出
export async function postForm(path: string, form: FormData): Promise<{ status: number; data: any }> {
  const resp = await fetch(path, { method: 'POST', headers: authHeader(), body: form })
  const data = await resp.json().catch(() => ({}))
  return { status: resp.status, data }
}
