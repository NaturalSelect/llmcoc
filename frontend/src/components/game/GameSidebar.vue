<script setup lang="ts">
import { Clock } from '@lucide/vue'
import { computed } from 'vue'
import type { CharacterCard, Session, SessionPlayer } from '@/api/types'
import SectionHeading from '@/components/base/SectionHeading.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import StatBar from '@/components/base/StatBar.vue'
import { playerTurnSubmitState } from '@/game/banner'
import type { HudFlash } from '@/game/hud'
import { hudChips, statNum, statusLabel } from '@/game/hud'
import type { Room, TimePoint } from '@/game/roomState'
import { portraitSrc } from '@/components/character/characterLogic'

// NOTE: 桌面端右侧栏，只展示信息，不做状态播报；播报仍由底部的 StatusDock 负责，这里不要加动态区域属性
const props = defineProps<{
  session: Session | null
  room: Room | null
  card: CharacterCard | null
  flash: HudFlash
  time: TimePoint | null
  userId: number | undefined
}>()
const emit = defineEmits<{ select: [player: SessionPlayer] }>()

const chips = computed(() => hudChips(props.card))
const occupation = computed(() => (props.card as { occupation?: string } | null)?.occupation || '未知职业')
const portrait = computed(() => portraitSrc(props.card as { portrait_hash?: string } | null))
const players = computed(() => props.session?.players ?? [])
const scenarioName = computed(() => (props.session?.scenario as { name?: string } | undefined)?.name || '')
const statusTone = computed(() => {
  const status = props.session?.status
  return status === 'playing' ? 'moss' : status === 'lobby' ? 'ledger' : 'faint'
})

function playerName(p: SessionPlayer): string {
  return p.character_card?.name || p.user?.username || ''
}
function playerOccupation(p: SessionPlayer): string {
  return (p.character_card as { occupation?: string } | null | undefined)?.occupation || '未知职业'
}
</script>

<template>
  <aside aria-label="房间信息">
    <div class="space-y-8 p-5">
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

      <section v-if="players.length" class="space-y-2">
        <SectionHeading :count="players.length">调查员</SectionHeading>
        <ul class="-mx-2 space-y-0.5">
          <li v-for="p in players" :key="p.user_id">
            <button
              type="button"
              class="flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left transition hover:bg-surface-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
              @click="emit('select', p)"
            >
              <span class="min-w-0 flex-1">
                <span class="block truncate text-sm font-medium text-fg">{{ playerName(p) }}</span>
                <span class="block truncate text-xs text-fg-subtle">{{ playerOccupation(p) }}</span>
              </span>
              <StampBadge v-if="playerTurnSubmitState(room, p) === 'submitted'" class="shrink-0" tone="moss">已提交</StampBadge>
              <StampBadge v-else-if="playerTurnSubmitState(room, p) === 'pending'" class="shrink-0" tone="faint">待提交</StampBadge>
              <StampBadge v-if="p.user_id === userId" class="shrink-0" tone="seal">你</StampBadge>
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
  </aside>
</template>
