<script setup lang="ts">
import { ArrowLeft, Ellipsis, IdCard, LogOut, Play, RotateCcw, Square, UserPlus, Users } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { Session } from '@/api/types'
import BaseButton from '@/components/base/BaseButton.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { isInSession, statusLabel } from '@/game/hud'

type Action = 'investigators' | 'myCard' | 'join' | 'leave' | 'start' | 'end' | 'revive'

const props = defineProps<{
  session: Session
  user: { id: number; role: string } | null
  busy: { leaving: boolean; ending: boolean; reviving: boolean }
}>()
const emit = defineEmits<{ action: [action: Action] }>()

const menuOpen = ref(false)
const menuRoot = ref<HTMLElement | null>(null)

const isCreator = computed(() => props.session.created_by === props.user?.id)
const isAdmin = computed(() => props.user?.role === 'admin')
const scenarioName = computed(() => (props.session.scenario as { name?: string } | undefined)?.name || '')

const actions = computed(() => {
  const s = props.session
  const list: Array<{ key: Action; label: string; tone: 'ghost' | 'green' | 'red'; disabled?: boolean }> = [
    { key: 'investigators', label: '调查员', tone: 'ghost' },
    { key: 'myCard', label: '我的角色', tone: 'ghost' },
  ]
  if (s.status === 'lobby' && !isInSession(s, props.user?.id)) list.push({ key: 'join', label: '带人物卡加入', tone: 'green' })
  if (s.status === 'lobby' && isInSession(s, props.user?.id)) list.push({ key: 'leave', label: '退出', tone: 'red', disabled: props.busy.leaving })
  if (s.status === 'lobby' && isCreator.value) list.push({ key: 'start', label: '开始', tone: 'green', disabled: !s.players?.length })
  if (s.status === 'playing' && (isCreator.value || isAdmin.value)) list.push({ key: 'end', label: '结束', tone: 'red', disabled: props.busy.ending })
  if (s.status === 'ended' && isAdmin.value) list.push({ key: 'revive', label: '复活', tone: 'green', disabled: props.busy.reviving })
  return list
})

// NOTE: 危险和成功操作用淡色底的 BaseButton 变体，不用实心色块；只有主操作才实心
const variantOf = { ghost: 'ghost', green: 'success', red: 'danger' } as const
const menuToneClass = { ghost: 'text-fg', green: 'text-success', red: 'text-danger' }
const iconOf = { investigators: Users, myCard: IdCard, join: UserPlus, leave: LogOut, start: Play, end: Square, revive: RotateCcw }

function run(action: Action) {
  menuOpen.value = false
  emit('action', action)
}

function onDocClick(e: MouseEvent) {
  if (menuRoot.value && !menuRoot.value.contains(e.target as Node)) menuOpen.value = false
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') menuOpen.value = false
}

watch(menuOpen, (open) => {
  if (open) {
    document.addEventListener('click', onDocClick)
    document.addEventListener('keydown', onKey)
  } else {
    document.removeEventListener('click', onDocClick)
    document.removeEventListener('keydown', onKey)
  }
})
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick)
  document.removeEventListener('keydown', onKey)
})
</script>

<template>
  <header class="z-10 flex h-14 shrink-0 items-center gap-3 border-b border-line bg-bg px-4">
    <RouterLink
      to="/sessions"
      class="inline-flex shrink-0 items-center gap-1.5 rounded-lg p-1 text-sm text-fg-subtle transition hover:text-fg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      aria-label="返回房间列表"
    >
      <ArrowLeft :size="16" :stroke-width="1.75" />
      <span class="hidden sm:inline">返回</span>
    </RouterLink>
    <span class="min-w-0 flex-1 truncate text-base font-semibold text-fg">{{ session.name }}</span>
    <span v-if="scenarioName" class="hidden shrink-0 truncate text-sm text-fg-subtle md:inline">{{ scenarioName }}</span>
    <StampBadge class="shrink-0" :tone="session.status === 'playing' ? 'moss' : session.status === 'lobby' ? 'ledger' : 'faint'">
      {{ statusLabel(session.status) }}
    </StampBadge>

    <div class="hidden shrink-0 items-center gap-2 sm:flex">
      <BaseButton v-for="a in actions" :key="a.key" size="sm" :variant="variantOf[a.tone]" :disabled="a.disabled" @click="run(a.key)">
        <component :is="iconOf[a.key]" :size="16" :stroke-width="1.75" />
        {{ a.label }}
      </BaseButton>
    </div>

    <div ref="menuRoot" class="relative shrink-0 sm:hidden">
      <BaseButton
        variant="ghost"
        size="sm"
        :aria-expanded="menuOpen"
        aria-controls="game-header-menu"
        aria-label="更多操作"
        @click="menuOpen = !menuOpen"
      >
        <Ellipsis :size="16" :stroke-width="1.75" />
      </BaseButton>
      <div
        v-if="menuOpen"
        id="game-header-menu"
        role="menu"
        class="absolute right-0 top-full z-50 mt-2 min-w-36 rounded-xl border border-line bg-surface-2 p-1 shadow-panel"
      >
        <button
          v-for="a in actions"
          :key="a.key"
          type="button"
          role="menuitem"
          class="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm transition hover:bg-surface-3 disabled:cursor-not-allowed disabled:opacity-50"
          :class="menuToneClass[a.tone]"
          :disabled="a.disabled"
          @click="run(a.key)"
        >
          <component :is="iconOf[a.key]" :size="16" :stroke-width="1.75" class="shrink-0" />
          {{ a.label }}
        </button>
      </div>
    </div>
  </header>
</template>
