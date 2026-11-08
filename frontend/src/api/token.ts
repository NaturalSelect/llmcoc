// NOTE: token 沿用旧前端的 localStorage 键，老用户升级后不用重新登录
const TOKEN_KEY = 'coc_token'

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || ''
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token)
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY)
}
