<script setup lang="ts">
import { computed } from 'vue'
import { BookOpen, Coins, LayoutDashboard, LogOut, ShieldCheck, ShoppingBag, Swords } from '@lucide/vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useCharactersStore } from '@/stores/characters'
import ConfirmHost from '@/components/base/ConfirmHost.vue'
import ToastHost from '@/components/base/ToastHost.vue'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const chars = useCharactersStore()

const showShell = computed(() => !route.meta.public)
const fullHeight = computed(() => !!route.meta.fullHeight)

const links = computed(() => {
  const base = [
    { to: '/', label: '首页', icon: LayoutDashboard, active: route.name === 'dashboard' },
    { to: '/sessions', label: '跑团大厅', icon: Swords, active: route.name === 'sessions' || route.name === 'game' },
    { to: '/shop', label: '商城', icon: ShoppingBag, active: route.name === 'shop' },
  ]
  return auth.isAdmin ? [...base, { to: '/admin', label: '管理', icon: ShieldCheck, active: route.name === 'admin' }] : base
})

async function logout() {
  auth.logout()
  chars.reset()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <div v-if="showShell" class="flex h-dvh flex-col overflow-hidden bg-bg text-fg">
    <nav class="z-40 flex h-14 shrink-0 items-center justify-between gap-2 border-b border-line bg-surface/80 px-3 backdrop-blur sm:px-4">
      <div class="nav-scroll flex min-w-0 flex-1 items-center gap-1">
        <RouterLink to="/" class="mr-2 flex shrink-0 items-center gap-2 font-semibold tracking-wide text-fg">
          <span class="flex h-7 w-7 items-center justify-center rounded-lg bg-accent/15 text-accent">
            <BookOpen :size="16" :stroke-width="1.75" />
          </span>
          <span class="hidden sm:inline">LLM-COC</span>
        </RouterLink>
        <RouterLink
          v-for="l in links"
          :key="l.to"
          :to="l.to"
          :data-active="l.active ? 'true' : undefined"
          class="flex h-9 shrink-0 items-center gap-1.5 rounded-lg px-3 text-sm transition"
          :class="l.active ? 'bg-surface-3 font-medium text-fg' : 'text-fg-muted hover:bg-surface-2 hover:text-fg'"
        >
          <component :is="l.icon" :size="16" :stroke-width="1.75" />
          {{ l.label }}
        </RouterLink>
      </div>
      <div class="flex shrink-0 items-center gap-2 text-sm sm:gap-3">
        <span class="flex items-center gap-1.5 whitespace-nowrap rounded-full bg-surface-2 px-2.5 py-1 text-fg-muted">
          <Coins :size="14" :stroke-width="1.75" class="text-accent" />
          <b class="font-semibold tabular-nums text-accent">{{ auth.user?.coins ?? 0 }}</b>
        </span>
        <span class="hidden whitespace-nowrap text-fg-muted sm:inline">
          人物卡 <b class="font-medium tabular-nums text-fg">{{ chars.characters.length }}</b>/{{ auth.user?.card_slots ?? 3 }}
        </span>
        <span class="hidden text-fg md:inline">{{ auth.user?.username }}</span>
        <button
          type="button"
          class="flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-fg-muted transition hover:bg-surface-2 hover:text-fg"
          @click="logout"
        >
          <LogOut :size="15" :stroke-width="1.75" />退出
        </button>
      </div>
    </nav>
    <main class="min-h-0 flex-1" :class="fullHeight ? 'flex flex-col overflow-hidden' : 'overflow-y-auto'">
      <RouterView />
    </main>
  </div>
  <RouterView v-else />
  <ToastHost />
  <ConfirmHost />
</template>

<style scoped>
.nav-scroll {
  overflow-x: auto;
  scrollbar-width: none;
}
.nav-scroll::-webkit-scrollbar {
  display: none;
}
</style>
