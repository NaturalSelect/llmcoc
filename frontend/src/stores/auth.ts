import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { clearToken, getToken, setToken } from '@/api/token'

export interface User {
  id: number
  username: string
  role: string
  coins?: number
  card_slots?: number
  [key: string]: unknown
}

export interface PublicSettings {
  requireInviteCode: boolean
  allowNSFW: boolean
}

// NOTE: 商城各项金币费率，由 GET /api/shop/costs 返回，字段按需读取
export type ShopCosts = Record<string, number>

interface AuthResponse {
  token: string
  user: User
}

export const useAuthStore = defineStore('auth', () => {
  const token = ref(getToken())
  const user = ref<User | null>(null)
  const publicSettings = ref<PublicSettings>({ requireInviteCode: false, allowNSFW: true })
  const shopCosts = ref<ShopCosts>({})
  // 已拉取过当前用户信息：路由守卫只在首次导航时调用 loadMe，之后用这个标记跳过
  const meLoaded = ref(false)

  const isLoggedIn = computed(() => !!token.value)
  const isAdmin = computed(() => user.value?.role === 'admin')

  async function loadPublicSettings(): Promise<void> {
    try {
      const ps = await api<{ require_invite_code?: boolean; allow_nsfw?: boolean }>('GET', '/api/auth/settings/public')
      publicSettings.value = { requireInviteCode: !!ps.require_invite_code, allowNSFW: ps.allow_nsfw !== false }
    } catch {
      // 拿不到公开设置时沿用默认值，不影响登录页渲染
    }
  }

  // NOTE: 商城费率不需要鉴权，失败时回落为空对象，调用方各自带默认值
  async function loadShopCosts(): Promise<void> {
    try {
      shopCosts.value = (await api<ShopCosts>('GET', '/api/shop/costs')) || {}
    } catch {
      shopCosts.value = {}
    }
  }

  async function loadMe(): Promise<void> {
    user.value = await api<User>('GET', '/api/auth/me')
    meLoaded.value = true
  }

  function applyAuth(r: AuthResponse): void {
    token.value = r.token
    setToken(r.token)
    user.value = r.user
    meLoaded.value = true
  }

  async function login(username: string, password: string): Promise<void> {
    applyAuth(await api<AuthResponse>('POST', '/api/auth/login', { username, password }))
  }

  async function register(form: { username: string; email: string; password: string; invite_code: string }): Promise<void> {
    applyAuth(await api<AuthResponse>('POST', '/api/auth/register', form))
  }

  function logout(): void {
    token.value = ''
    clearToken()
    user.value = null
    meLoaded.value = false
  }

  return {
    token,
    user,
    publicSettings,
    shopCosts,
    meLoaded,
    isLoggedIn,
    isAdmin,
    loadPublicSettings,
    loadShopCosts,
    loadMe,
    login,
    register,
    logout,
  }
})
