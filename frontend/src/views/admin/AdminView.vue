<script setup lang="ts">
import { computed, defineAsyncComponent } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageHeader from '@/components/base/PageHeader.vue'
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
    <PageHeader title="管理后台" />
    <!-- NOTE: 滚动容器是 App 的 main，sticky 吸在它的顶部；底色必须不透明，否则内容会从标签栏下面透出来 -->
    <div class="sticky top-0 z-10 -mx-5 mt-4 mb-5 bg-bg px-5">
      <TabBar v-model="active" :tabs="[...ADMIN_TABS]" />
    </div>
    <component :is="panels[active]" :key="active" />
  </div>
</template>
