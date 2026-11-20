<script setup lang="ts">
import { BookMarked, Clock, MapPin, MessageCircle, Sparkles, Users } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import type { CharacterCard, Session, SessionPlayer } from '@/api/types'
import SectionHeading from '@/components/base/SectionHeading.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import StatBar from '@/components/base/StatBar.vue'
import { portraitSrc } from '@/components/character/characterLogic'
import CompanionActions from '@/components/game/CompanionActions.vue'
import CompanionBadge from '@/components/game/CompanionBadge.vue'
import HandbookPanel from '@/components/game/HandbookPanel.vue'
import TableTalkPanel from '@/components/game/TableTalkPanel.vue'
import { playerTurnSubmitState } from '@/game/banner'
import { isCompanionPlayer } from '@/game/companion'
import type { HandbookData } from '@/game/handbookTypes'
import type { HudFlash } from '@/game/hud'
import { hudChips, isInSession, statNum, statusLabel } from '@/game/hud'
import type { Room, TimePoint } from '@/game/roomState'
import { useAuthStore } from '@/stores/auth'

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
  // NOTE: AI 陪玩的增删入口是否可见由游戏页算好传入（房主/管理员、lobby、有无 AI、是否满员）；点击后的确认和请求也在游戏页里
  canAddCompanion?: boolean
  canRemoveCompanion?: boolean
  companionBusy?: boolean
}>()
const emit = defineEmits<{
  select: [player: SessionPlayer]
  refreshHandbook: []
  addCompanion: []
  removeCompanion: []
}>()

const auth = useAuthStore()

const activeTab = ref<'status' | 'handbook' | 'ooc'>('status')
const chips = computed(() => hudChips(props.card))
const hasLuck = computed(() => props.card?.stats?.luck !== undefined)
const occupation = computed(() => (props.card as { occupation?: string } | null)?.occupation || '未知职业')
const portrait = computed(() => portraitSrc(props.card as { portrait_hash?: string } | null))
const players = computed(() => props.session?.players ?? [])
const canPostOoc = computed(() =>
  Boolean(props.session && (isInSession(props.session, props.userId) || props.session.created_by === props.userId || auth.isAdmin)),
)
const scenarioName = computed(() => (props.session?.scenario as { name?: string } | undefined)?.name || '')
const statusTone = computed(() => {
  const status = props.session?.status
  return status === 'playing' ? 'moss' : status === 'lobby' ? 'ledger' : 'faint'
})
const oocUnread = computed(() => props.room?.oocUnread ?? 0)

watch(
  () => props.session?.id,
  () => {
    activeTab.value = 'status'
  },
)

watch(activeTab, (tab) => {
  if (tab === 'handbook') emit('refreshHandbook')
})

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
        </template>
        <p v-if="time" class="flex items-center gap-2 text-sm tabular-nums text-fg-muted" :title="time.elapsed || undefined">
          <Clock :size="16" :stroke-width="1.75" class="shrink-0 text-fg-subtle" />
          {{ time.label }}
        </p>
      </section>

      <section v-if="players.length || canAddCompanion" class="space-y-2">
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
                <CompanionBadge v-if="isCompanionPlayer(p)" class="shrink-0" />
                <StampBadge v-if="p.handout_code" class="shrink-0" tone="ledger">{{ p.handout_code }}</StampBadge>
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
        <CompanionActions
          :can-add="Boolean(canAddCompanion)"
          :can-remove="Boolean(canRemoveCompanion)"
          :busy="companionBusy"
          @add="emit('addCompanion')"
          @remove="emit('removeCompanion')"
        />
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
