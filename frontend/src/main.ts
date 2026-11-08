import { createPinia } from 'pinia'
import { createApp } from 'vue'
import App from './App.vue'
import { setUnauthorizedHandler } from './api/client'
import { createAppRouter } from './router'
import { useAuthStore } from './stores/auth'
import { useCharactersStore } from './stores/characters'
import { useUiStore } from './stores/ui'
import './styles/theme.css'

const app = createApp(App)
const pinia = createPinia()
const router = createAppRouter()
app.use(pinia)
app.use(router)

// NOTE: 任何带 token 的请求返回 401 都视为登录失效：清登录态、提示并回到登录页，保留当前地址以便登录后回来。
// 多个请求同时 401 时只处理一次
let handlingUnauthorized = false
setUnauthorizedHandler(() => {
  if (handlingUnauthorized) return
  handlingUnauthorized = true
  const auth = useAuthStore(pinia)
  const redirect = router.currentRoute.value.fullPath
  auth.logout()
  useCharactersStore(pinia).reset()
  useUiStore(pinia).toast('登录已失效，请重新登录', 'error')
  void router
    .replace({ name: 'login', query: redirect === '/' || redirect.startsWith('/login') ? {} : { redirect } })
    .finally(() => {
      handlingUnauthorized = false
    })
})

app.mount('#app')
