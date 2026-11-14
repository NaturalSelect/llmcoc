import { createRouter, createWebHistory } from 'vue-router'
import type { RouteLocationNormalized, Router, RouterHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

export const routes = [
  { path: '/login', name: 'login', component: () => import('@/views/AuthView.vue'), meta: { public: true } },
  { path: '/', name: 'dashboard', component: () => import('@/views/DashboardView.vue') },
  { path: '/sessions', name: 'sessions', component: () => import('@/views/SessionsView.vue') },
  { path: '/sessions/:id(\\d+)', name: 'game', component: () => import('@/views/GameView.vue'), meta: { fullHeight: true } },
  { path: '/shop', name: 'shop', component: () => import('@/views/ShopView.vue') },
  // NOTE: 必须声明在 /admin/:tab? 之前，否则 online-tasks 会被当成 tab 参数吞掉
  { path: '/admin/online-tasks', name: 'admin-online-tasks', component: () => import('@/views/admin/OnlineTasksView.vue'), meta: { admin: true } },
  { path: '/admin/:tab?', name: 'admin', component: () => import('@/views/admin/AdminView.vue'), meta: { admin: true } },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

// 登录后默认落点；redirect 只接受站内相对路径，避免被构造成开放重定向
export function safeRedirect(raw: unknown): string {
  return typeof raw === 'string' && raw.startsWith('/') && !raw.startsWith('//') && !raw.startsWith('/login') ? raw : '/'
}

export async function authGuard(to: RouteLocationNormalized) {
  const auth = useAuthStore()

  if (to.meta.public) {
    return auth.isLoggedIn && to.name === 'login' ? { path: safeRedirect(to.query.redirect) } : true
  }
  if (!auth.isLoggedIn) {
    return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
  }
  // 首次导航（含刷新）时核对 token：失败说明 token 已失效，清掉后回登录页
  if (!auth.meLoaded) {
    try {
      await auth.loadMe()
    } catch {
      auth.logout()
      return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
    }
  }
  if (to.meta.admin && !auth.isAdmin) return { name: 'dashboard' }
  return true
}

export function createAppRouter(history: RouterHistory = createWebHistory()): Router {
  const router = createRouter({ history, routes })
  router.beforeEach(authGuard)
  return router
}
