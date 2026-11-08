<script setup lang="ts">
import { computed } from 'vue'
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
    { to: '/', label: '首页', active: route.name === 'dashboard' },
    { to: '/sessions', label: '跑团大厅', active: route.name === 'sessions' || route.name === 'game' },
    { to: '/shop', label: '商城', active: route.name === 'shop' },
  ]
  return auth.isAdmin ? [...base, { to: '/admin', label: '管理', active: route.name === 'admin' }] : base
})

async function logout() {
  auth.logout()
  chars.reset()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <div v-if="showShell" class="flex h-dvh flex-col overflow-hidden bg-paper text-ink">
    <nav class="z-40 flex shrink-0 items-center justify-between gap-2 border-b border-paper-edge bg-paper-deep px-3 py-2.5 sm:px-4">
      <div class="nav-scroll flex min-w-0 flex-1 items-center gap-1">
        <RouterLink to="/" class="mr-1 shrink-0 font-type text-base font-bold tracking-widest text-seal sm:mr-2">COC</RouterLink>
        <RouterLink
          v-for="l in links"
          :key="l.to"
          :to="l.to"
          class="shrink-0 rounded-sm px-2.5 py-1 font-serif text-sm transition"
          :class="l.active ? 'bg-paper text-seal underline decoration-seal decoration-2 underline-offset-4' : 'text-ink-soft hover:text-ink'"
        >
          {{ l.label }}
        </RouterLink>
      </div>
      <div class="flex shrink-0 items-center gap-2.5 font-type text-xs sm:gap-3">
        <span class="whitespace-nowrap text-ink-soft">金币 <b class="text-seal">{{ auth.user?.coins ?? 0 }}</b></span>
        <span class="hidden whitespace-nowrap text-ink-soft sm:inline">
          人物卡 <b class="text-ink">{{ chars.characters.length }}</b>/{{ auth.user?.card_slots ?? 3 }}
        </span>
        <span class="hidden text-ink md:inline">{{ auth.user?.username }}</span>
        <button type="button" class="text-ink-faint transition hover:text-seal" @click="logout">退出</button>
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
