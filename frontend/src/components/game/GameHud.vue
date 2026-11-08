<script setup lang="ts">
import { computed } from 'vue'
import type { CharacterCard } from '@/api/types'
import StampBadge from '@/components/base/StampBadge.vue'
import StatBar from '@/components/base/StatBar.vue'
import type { HudFlash } from '@/game/hud'
import { hudChips, statNum } from '@/game/hud'
import type { TimePoint } from '@/game/roomState'

const props = defineProps<{ card: CharacterCard; flash: HudFlash; time: TimePoint | null }>()
const chips = computed(() => hudChips(props.card))
</script>

<template>
  <div class="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-b border-paper-edge bg-paper-deep/50 px-3 py-1.5">
    <span class="max-w-[8rem] truncate font-serif text-sm font-bold text-ink">{{ card.name || '调查员' }}</span>
    <StatBar class="w-36" label="HP" tone="moss" :value="statNum(card, 'hp')" :max="statNum(card, 'max_hp')" :flash="flash.hp" />
    <StatBar class="w-36" label="SAN" tone="seal" :value="statNum(card, 'san')" :max="statNum(card, 'max_san')" :flash="flash.san" />
    <StatBar class="hidden w-36 sm:flex" label="MP" tone="ledger" :value="statNum(card, 'mp')" :max="statNum(card, 'max_mp')" />
    <StampBadge v-for="chip in chips" :key="chip.text" tone="seal" :title="chip.title || undefined">{{ chip.text }}</StampBadge>
    <span v-if="time" class="ml-auto font-type text-xs text-ink-soft" :title="time.elapsed || undefined">🕯 {{ time.label }}</span>
  </div>
</template>
