<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { Session } from '@/api/types'
import StampBadge from '@/components/base/StampBadge.vue'
import { isInSession, statusLabel } from '@/game/hud'

type Action = 'investigators' | 'myCard' | 'leave' | 'start' | 'end' | 'revive'

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
  if (s.status === 'lobby' && isInSession(s, props.user?.id)) list.push({ key: 'leave', label: '退出', tone: 'red', disabled: props.busy.leaving })
  if (s.status === 'lobby' && isCreator.value) list.push({ key: 'start', label: '▶ 开始', tone: 'green' })
  if (s.status === 'playing' && (isCreator.value || isAdmin.value)) list.push({ key: 'end', label: '结束', tone: 'red', disabled: props.busy.ending })
  if (s.status === 'ended' && isAdmin.value) list.push({ key: 'revive', label: '复活', tone: 'green', disabled: props.busy.reviving })
  return list
})

const toneClass = {
  ghost: 'border-paper-edge text-ink-soft hover:bg-paper-deep',
  green: 'border-moss text-moss hover:bg-moss/10',
  red: 'border-seal text-seal hover:bg-seal/10',
}

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
  <header class="z-10 flex shrink-0 items-center gap-2 border-b border-paper-edge bg-paper px-3 py-2 text-sm">
    <RouterLink to="/sessions" class="shrink-0 text-ink-faint transition hover:text-seal" aria-label="返回房间列表">← 返回</RouterLink>
    <span class="min-w-0 flex-1 truncate font-serif font-bold text-ink">{{ session.name }}</span>
    <span v-if="scenarioName" class="hidden shrink-0 truncate text-xs text-ink-faint md:inline">{{ scenarioName }}</span>
    <StampBadge class="shrink-0" :tone="session.status === 'playing' ? 'moss' : session.status === 'lobby' ? 'ledger' : 'faint'">
      {{ statusLabel(session.status) }}
    </StampBadge>

    <div class="hidden shrink-0 items-center gap-1.5 sm:flex">
      <button
        v-for="a in actions"
        :key="a.key"
        type="button"
        class="rounded-sm border px-2.5 py-1 text-xs transition disabled:cursor-not-allowed disabled:opacity-50"
        :class="toneClass[a.tone]"
        :disabled="a.disabled"
        @click="run(a.key)"
      >
        {{ a.label }}
      </button>
    </div>

    <div ref="menuRoot" class="relative shrink-0 sm:hidden">
      <button
        type="button"
        class="rounded-sm border border-paper-edge px-2.5 py-1.5 text-xs text-ink-soft"
        :aria-expanded="menuOpen"
        aria-controls="game-header-menu"
        aria-label="更多操作"
        @click="menuOpen = !menuOpen"
      >
        ⋯
      </button>
      <div
        v-if="menuOpen"
        id="game-header-menu"
        role="menu"
        class="absolute right-0 top-full z-50 mt-1 min-w-[6.5rem] rounded-sm border border-paper-edge bg-paper py-1 shadow-xl"
      >
        <button
          v-for="a in actions"
          :key="a.key"
          type="button"
          role="menuitem"
          class="block w-full px-3 py-2 text-left text-sm text-ink hover:bg-paper-deep disabled:opacity-50"
          :disabled="a.disabled"
          @click="run(a.key)"
        >
          {{ a.label }}
        </button>
      </div>
    </div>
  </header>
</template>
