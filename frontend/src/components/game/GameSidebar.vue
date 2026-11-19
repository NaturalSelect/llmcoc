<script setup lang="ts">
import { BookMarked, ChevronDown, Clock, Lock, MapPin, MessageCircle, Sparkles, Users } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { CharacterCard, Session, SessionPlayer } from '@/api/types'
import BaseButton from '@/components/base/BaseButton.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import StatBar from '@/components/base/StatBar.vue'
import { portraitSrc } from '@/components/character/characterLogic'
import HandbookPanel from '@/components/game/HandbookPanel.vue'
import TableTalkPanel from '@/components/game/TableTalkPanel.vue'
import { playerTurnSubmitState } from '@/game/banner'
import type { HandbookData } from '@/game/handbookTypes'
import type { HudFlash } from '@/game/hud'
import { hudChips, isInSession, statNum, statusLabel } from '@/game/hud'
import type { Room, TimePoint } from '@/game/roomState'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'

// NOTE: 桌面端右侧栏，只展示信息，不做状态播报；播报仍由底部的 StatusDock 负责，这里不要加动态区域属性
const props = defineProps<{
  session: Session | null
  room: Room | null
  card: CharacterCard | null
  flash: HudFlash
  time: TimePoint | null
  userId: number | undefined
  handbook: HandbookData
  handbookLoading: boolean
}>()
const emit = defineEmits<{ select: [player: SessionPlayer]; refresh: []; refreshHandbook: [] }>()

const auth = useAuthStore()
const ui = useUiStore()

const activeTab = ref<'status' | 'handbook' | 'ooc'>('status')
const chips = computed(() => hudChips(props.card))
const hasLuck = computed(() => props.card?.stats?.luck !== undefined)
const occupation = computed(() => (props.card as { occupation?: string } | null)?.occupation || '未知职业')
const portrait = computed(() => portraitSrc(props.card as { portrait_hash?: string } | null))
const players = computed(() => props.session?.players ?? [])
const me = computed(() => players.value.find((p) => p.user_id === props.userId) ?? null)
const mySecret = computed(() => me.value?.secret_background || '')
const canPostOoc = computed(() =>
  Boolean(props.session && (isInSession(props.session, props.userId) || props.session.created_by === props.userId || auth.isAdmin)),
)
const canEditSecret = computed(() => {
  if (!me.value || !props.session) return false
  if (props.session.status === 'lobby') return true
  if (props.session.status === 'playing') return props.session.created_by === props.userId || auth.isAdmin
  return false
})
const showSecretCard = computed(() => Boolean(me.value && (mySecret.value || canEditSecret.value)))
const scenarioName = computed(() => (props.session?.scenario as { name?: string } | undefined)?.name || '')
const statusTone = computed(() => {
  const status = props.session?.status
  return status === 'playing' ? 'moss' : status === 'lobby' ? 'ledger' : 'faint'
})
const oocUnread = computed(() => props.room?.oocUnread ?? 0)

const secretOpen = ref(false)
const secretEditing = ref(false)
const secretDraft = ref('')
const secretSaving = ref(false)

watch(
  () => props.session?.id,
  () => {
    activeTab.value = 'status'
    secretOpen.value = false
    secretEditing.value = false
  },
)

watch(activeTab, (tab) => {
  if (tab === 'handbook') emit('refreshHandbook')
})

function startEditSecret() {
  secretDraft.value = mySecret.value
  secretEditing.value = true
  secretOpen.value = true
}

async function saveSecret() {
  if (!props.session || secretSaving.value) return
  secretSaving.value = true
  try {
    await api<{ secret_background?: string }>('PUT', `/api/sessions/${props.session.id}/secret`, {
      secret_background: secretDraft.value,
    })
    secretEditing.value = false
    ui.toast('秘密背景已更新')
    emit('refresh')
  } catch (e) {
    ui.toast(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    secretSaving.value = false
  }
}

function playerName(p: SessionPlayer): string {
  return p.character_card?.name || p.user?.username || ''
}
function playerOccupation(p: SessionPlayer): string {
  return (p.character_card as { occupation?: string } | null | undefined)?.occupation || '未知职业'
}
function playerPublicChips(p: SessionPlayer) {
  return hudChips(p.character_card ?? null)
}
</script>

<template>
  <aside aria-label="房间信息">
    <div class="flex shrink-0 border-b border-line px-2 pt-2">
      <button
        type="button"
        class="inline-flex flex-1 items-center justify-center gap-1 border-b-2 px-1.5 py-2 text-xs font-medium transition"
        :class="activeTab === 'status' ? 'border-accent text-fg' : 'border-transparent text-fg-subtle hover:text-fg'"
        @click="activeTab = 'status'"
      >
        <Users :size="14" :stroke-width="1.75" class="shrink-0" />
        状态
      </button>
      <button
        type="button"
        class="inline-flex flex-1 items-center justify-center gap-1 border-b-2 px-1.5 py-2 text-xs font-medium transition"
        :class="activeTab === 'handbook' ? 'border-accent text-fg' : 'border-transparent text-fg-subtle hover:text-fg'"
        @click="activeTab = 'handbook'"
      >
        <BookMarked :size="14" :stroke-width="1.75" class="shrink-0" />
        手册
        <span v-if="handbook.clues.length" class="tabular-nums text-fg-subtle">{{ handbook.clues.length }}</span>
      </button>
      <button
        type="button"
        class="inline-flex flex-1 items-center justify-center gap-1 border-b-2 px-1.5 py-2 text-xs font-medium transition"
        :class="activeTab === 'ooc' ? 'border-accent text-fg' : 'border-transparent text-fg-subtle hover:text-fg'"
        @click="activeTab = 'ooc'"
      >
        <MessageCircle :size="14" :stroke-width="1.75" class="shrink-0" />
        桌边
        <span
          v-if="oocUnread > 0 && activeTab !== 'ooc'"
          class="inline-flex min-w-4 items-center justify-center rounded-full bg-accent px-1 text-xs font-semibold tabular-nums text-bg"
        >
          {{ oocUnread > 99 ? '99+' : oocUnread }}
        </span>
      </button>
    </div>

    <div v-show="activeTab === 'status'" class="min-h-0 flex-1 space-y-8 overflow-y-auto p-5">
      <section v-if="card || time" class="space-y-4">
        <template v-if="card">
          <div class="flex min-w-0 items-center gap-3">
            <img v-if="portrait" :src="portrait" alt="" class="h-20 w-14 shrink-0 rounded-lg bg-surface-2 object-cover object-top" />
            <div class="min-w-0 flex-1">
              <p class="truncate text-base font-semibold text-fg">{{ card.name || '调查员' }}</p>
              <p class="mt-0.5 truncate text-sm text-fg-subtle">{{ occupation }}</p>
            </div>
          </div>
          <div class="space-y-2">
            <StatBar label="HP" tone="hp" :value="statNum(card, 'hp')" :max="statNum(card, 'max_hp')" :flash="flash.hp" />
            <StatBar label="SAN" tone="san" :value="statNum(card, 'san')" :max="statNum(card, 'max_san')" :flash="flash.san" />
            <StatBar label="MP" tone="mp" :value="statNum(card, 'mp')" :max="statNum(card, 'max_mp')" />
            <div v-if="hasLuck" class="flex items-center justify-between rounded-md bg-surface-2 px-2.5 py-1.5 text-xs">
              <span class="inline-flex items-center gap-1.5 text-fg-muted">
                <Sparkles :size="14" :stroke-width="1.75" class="shrink-0 text-accent" />
                幸运
              </span>
              <span class="font-medium tabular-nums text-fg">{{ statNum(card, 'luck') }}</span>
            </div>
          </div>
          <div v-if="chips.length" class="flex flex-wrap gap-1.5">
            <StampBadge v-for="chip in chips" :key="chip.text" tone="seal" :title="chip.title || undefined">{{ chip.text }}</StampBadge>
          </div>
          <div v-if="showSecretCard" class="rounded-lg border border-info/30 bg-info/10 px-3 py-2">
            <button
              type="button"
              class="flex w-full items-center justify-between gap-2 text-left text-xs font-medium text-info"
              :aria-expanded="secretOpen"
              @click="secretOpen = !secretOpen"
            >
              <span class="inline-flex items-center gap-1.5">
                <Lock :size="14" :stroke-width="1.75" class="shrink-0" />
                秘密背景 (HO)
              </span>
              <span class="inline-flex items-center gap-1 text-fg-subtle">
                <span v-if="mySecret">已设</span>
                <ChevronDown :size="14" :stroke-width="1.75" class="shrink-0 transition-transform" :class="secretOpen ? 'rotate-180' : ''" />
              </span>
            </button>
            <div v-if="secretOpen" class="mt-2 space-y-2">
              <form v-if="secretEditing" class="space-y-2" @submit.prevent="saveSecret">
                <input
                  v-model="secretDraft"
                  type="text"
                  maxlength="1000"
                  placeholder="仅你与 KP 可见的隐藏身份或任务…"
                  aria-label="编辑秘密背景"
                  class="w-full rounded-md border border-line bg-surface px-2.5 py-1.5 text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
                />
                <div class="flex justify-end gap-1.5">
                  <BaseButton variant="ghost" size="sm" @click="secretEditing = false">取消</BaseButton>
                  <BaseButton type="submit" size="sm" :loading="secretSaving">保存</BaseButton>
                </div>
              </form>
              <template v-else>
                <p v-if="mySecret" class="whitespace-pre-wrap text-xs leading-relaxed text-fg">{{ mySecret }}</p>
                <p v-else class="text-xs text-fg-subtle">未填写，仅你与 KP 可见。</p>
                <button
                  v-if="canEditSecret"
                  type="button"
                  class="text-xs text-info underline-offset-2 hover:underline"
                  @click="startEditSecret"
                >
                  {{ mySecret ? '修改秘密背景' : '填写秘密背景' }}
                </button>
              </template>
            </div>
          </div>
        </template>
        <p v-if="time" class="flex items-center gap-2 text-sm tabular-nums text-fg-muted" :title="time.elapsed || undefined">
          <Clock :size="16" :stroke-width="1.75" class="shrink-0 text-fg-subtle" />
          {{ time.label }}
        </p>
      </section>

      <section v-if="players.length" class="space-y-2">
        <SectionHeading :count="players.length">调查员</SectionHeading>
        <ul class="-mx-2 space-y-0.5">
          <li v-for="p in players" :key="p.user_id">
            <button
              type="button"
              class="flex w-full flex-col gap-1 rounded-lg px-2 py-2 text-left transition hover:bg-surface-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
              @click="emit('select', p)"
            >
              <div class="flex w-full items-center gap-2">
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-sm font-medium text-fg">{{ playerName(p) }}</span>
                  <span class="block truncate text-xs text-fg-subtle">{{ playerOccupation(p) }}</span>
                </span>
                <StampBadge v-if="playerTurnSubmitState(room, p) === 'submitted'" class="shrink-0" tone="moss">已提交</StampBadge>
                <StampBadge v-else-if="playerTurnSubmitState(room, p) === 'pending'" class="shrink-0" tone="faint">待提交</StampBadge>
                <StampBadge v-if="p.user_id === userId" class="shrink-0" tone="seal">你</StampBadge>
              </div>
              <div v-if="p.location || playerPublicChips(p).length" class="flex flex-wrap items-center gap-1.5">
                <span v-if="p.location" class="inline-flex max-w-full items-center gap-1 truncate text-xs text-fg-muted">
                  <MapPin :size="12" :stroke-width="1.75" class="shrink-0 text-fg-subtle" />
                  <span class="truncate">{{ p.location }}</span>
                </span>
                <StampBadge v-for="chip in playerPublicChips(p)" :key="chip.text" tone="seal" :title="chip.title || undefined">
                  {{ chip.text }}
                </StampBadge>
              </div>
            </button>
          </li>
        </ul>
      </section>

      <section v-if="session" class="space-y-2">
        <SectionHeading>房间</SectionHeading>
        <div class="flex items-center justify-between gap-2">
          <span class="min-w-0 flex-1 truncate text-sm text-fg-muted">{{ scenarioName }}</span>
          <StampBadge class="shrink-0" :tone="statusTone">{{ statusLabel(session.status) }}</StampBadge>
        </div>
      </section>
    </div>

    <HandbookPanel
      v-show="activeTab === 'handbook'"
      :session="session"
      :data="handbook"
      :loading="handbookLoading"
      :user-id="userId"
      :can-edit="canPostOoc"
      @refresh="emit('refreshHandbook')"
    />

    <TableTalkPanel
      v-show="activeTab === 'ooc'"
      :session="session"
      :room="room"
      :user-id="userId"
      :can-post="canPostOoc"
      :active="activeTab === 'ooc'"
    />
  </aside>
</template>
