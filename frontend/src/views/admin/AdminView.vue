<script setup lang="ts">
import { computed, defineAsyncComponent } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import TabBar from '@/components/base/TabBar.vue'
import { ADMIN_TABS, resolveAdminTab } from './adminTabs'
import type { AdminTabKey } from './adminTabs'

const route = useRoute()
const router = useRouter()

// NOTE: 每个 tab 单独拆包，只有打开时才下载；切换 tab 时旧组件卸载，所以各 tab 自己在 onMounted 里加载数据
const panels: Record<AdminTabKey, ReturnType<typeof defineAsyncComponent>> = {
  users: defineAsyncComponent(() => import('./UsersTab.vue')),
  providers: defineAsyncComponent(() => import('./ProvidersTab.vue')),
  agents: defineAsyncComponent(() => import('./AgentsTab.vue')),
  scenarios: defineAsyncComponent(() => import('./ScenariosTab.vue')),
  shop: defineAsyncComponent(() => import('./ShopTab.vue')),
  announcements: defineAsyncComponent(() => import('./AnnouncementsTab.vue')),
  settings: defineAsyncComponent(() => import('./SettingsTab.vue')),
  cache: defineAsyncComponent(() => import('./CacheTab.vue')),
}

const active = computed<AdminTabKey>({
  get: () => resolveAdminTab(route.params.tab),
  set: (key) => void router.push({ name: 'admin', params: { tab: key } }),
})
</script>

<template>
  <div class="mx-auto w-full max-w-6xl p-5">
    <h2 class="mb-4 font-serif text-xl font-bold text-seal">管理后台</h2>
    <TabBar v-model="active" :tabs="[...ADMIN_TABS]" class="mb-5" />
    <component :is="panels[active]" :key="active" />
  </div>
</template>
