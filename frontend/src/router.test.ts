// @vitest-environment jsdom
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { createAppRouter, safeRedirect } from './router'
import { useAuthStore } from './stores/auth'

function mockApi(handlers: Record<string, () => { ok: boolean; status?: number; body: unknown }>) {
  const fn = vi.fn(async (path: string) => {
    const h = handlers[path]
    if (!h) throw new Error('unexpected request ' + path)
    const r = h()
    return { ok: r.ok, status: r.status ?? (r.ok ? 200 : 500), json: async () => r.body } as Response
  })
  vi.stubGlobal('fetch', fn)
  return fn
}

const ME_OK = { '/api/auth/me': () => ({ ok: true, body: { id: 1, username: 'a', role: 'user' } }) }
const ME_ADMIN = { '/api/auth/me': () => ({ ok: true, body: { id: 1, username: 'a', role: 'admin' } }) }
const ME_401 = { '/api/auth/me': () => ({ ok: false, status: 401, body: { error: '未登录' } }) }

async function boot(path: string) {
  const router = createAppRouter(createMemoryHistory())
  await router.push(path)
  await router.isReady()
  return router
}

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  vi.unstubAllGlobals()
})

describe('safeRedirect', () => {
  test('只接受站内相对路径', () => {
    expect(safeRedirect('/sessions/3')).toBe('/sessions/3')
    expect(safeRedirect('/admin/users?x=1')).toBe('/admin/users?x=1')
    expect(safeRedirect('//evil.com')).toBe('/')
    expect(safeRedirect('https://evil.com')).toBe('/')
    expect(safeRedirect('/login')).toBe('/')
    expect(safeRedirect(undefined)).toBe('/')
    expect(safeRedirect(['/a'])).toBe('/')
  })
})

describe('路由守卫', () => {
  test('未登录访问受保护页：跳到登录页并带上 redirect', async () => {
    const router = await boot('/sessions/12')
    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query.redirect).toBe('/sessions/12')
  })

  test('未登录访问首页：不带多余的 redirect', async () => {
    const router = await boot('/')
    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query.redirect).toBeUndefined()
  })

  test('刷新时 token 有效：首次导航拉取用户信息，之后不再重复拉取', async () => {
    localStorage.setItem('coc_token', 't')
    setActivePinia(createPinia())
    const fetchFn = mockApi(ME_OK)
    const router = await boot('/sessions/12')
    expect(router.currentRoute.value.fullPath).toBe('/sessions/12')
    expect(useAuthStore().user?.username).toBe('a')
    await router.push('/shop')
    await router.push('/')
    expect(fetchFn).toHaveBeenCalledTimes(1)
  })

  test('刷新时 token 已失效：清掉 token 并回到登录页，保留要去的地址', async () => {
    localStorage.setItem('coc_token', 'stale')
    setActivePinia(createPinia())
    mockApi(ME_401)
    const router = await boot('/shop')
    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query.redirect).toBe('/shop')
    expect(localStorage.getItem('coc_token')).toBeNull()
    expect(useAuthStore().isLoggedIn).toBe(false)
  })

  test('已登录访问登录页：回到 redirect 指向的站内地址，非法地址回首页', async () => {
    localStorage.setItem('coc_token', 't')
    setActivePinia(createPinia())
    mockApi(ME_OK)
    const router = await boot('/login?redirect=/sessions/3')
    expect(router.currentRoute.value.fullPath).toBe('/sessions/3')
    await router.push('/login?redirect=//evil.com')
    expect(router.currentRoute.value.fullPath).toBe('/')
  })

  test('非管理员访问 /admin 被拦回首页，管理员可以进入并保留子页签', async () => {
    localStorage.setItem('coc_token', 't')
    setActivePinia(createPinia())
    mockApi(ME_OK)
    const router = await boot('/admin/users')
    expect(router.currentRoute.value.name).toBe('dashboard')

    setActivePinia(createPinia())
    mockApi(ME_ADMIN)
    const admin = await boot('/admin/providers')
    expect(admin.currentRoute.value.name).toBe('admin')
    expect(admin.currentRoute.value.params.tab).toBe('providers')
  })

  test('游戏页路由只匹配数字 id，其余路径回首页', async () => {
    localStorage.setItem('coc_token', 't')
    setActivePinia(createPinia())
    mockApi(ME_OK)
    const router = await boot('/sessions/12')
    expect(router.currentRoute.value.name).toBe('game')
    expect(router.currentRoute.value.params.id).toBe('12')
    await router.push('/nowhere/at/all')
    expect(router.currentRoute.value.name).toBe('dashboard')
  })
})

describe('auth store', () => {
  test('登录写入 token 和用户，退出清空', async () => {
    mockApi({ '/api/auth/login': () => ({ ok: true, body: { token: 'tok', user: { id: 2, username: 'b', role: 'user' } } }) })
    const auth = useAuthStore()
    await auth.login('b', 'pw')
    expect(auth.isLoggedIn).toBe(true)
    expect(auth.meLoaded).toBe(true)
    expect(localStorage.getItem('coc_token')).toBe('tok')
    expect(auth.user?.username).toBe('b')

    auth.logout()
    expect(auth.isLoggedIn).toBe(false)
    expect(auth.user).toBeNull()
    expect(auth.meLoaded).toBe(false)
    expect(localStorage.getItem('coc_token')).toBeNull()
  })

  test('登录失败抛出后端错误信息且不写入 token', async () => {
    mockApi({ '/api/auth/login': () => ({ ok: false, status: 401, body: { error: '用户名或密码错误' } }) })
    const auth = useAuthStore()
    await expect(auth.login('b', 'bad')).rejects.toThrow('用户名或密码错误')
    expect(auth.isLoggedIn).toBe(false)
    expect(localStorage.getItem('coc_token')).toBeNull()
  })

  test('公开设置和费率：失败时沿用默认值', async () => {
    mockApi({
      '/api/auth/settings/public': () => ({ ok: true, body: { require_invite_code: true, allow_nsfw: false } }),
      '/api/shop/costs': () => ({ ok: false, status: 500, body: {} }),
    })
    const auth = useAuthStore()
    await auth.loadPublicSettings()
    expect(auth.publicSettings).toStrictEqual({ requireInviteCode: true, allowNSFW: false })
    await auth.loadShopCosts()
    expect(auth.shopCosts).toStrictEqual({})

    mockApi({ '/api/auth/settings/public': () => ({ ok: true, body: {} }) })
    await auth.loadPublicSettings()
    expect(auth.publicSettings.allowNSFW).toBe(true)
  })
})
