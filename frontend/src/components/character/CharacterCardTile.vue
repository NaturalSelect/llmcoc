<script setup lang="ts">
import type { CharacterCardFull } from '@/api/characterTypes'
import PaperCard from '@/components/base/PaperCard.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import StatBar from '@/components/base/StatBar.vue'
import { cardSubtitle, hasMadness, madnessLabel } from './characterLogic'
import StatCell from './StatCell.vue'

defineProps<{ card: CharacterCardFull }>()
const emit = defineEmits<{ open: [card: CharacterCardFull]; delete: [card: CharacterCardFull] }>()

const HEADLINE_STATS = [
  ['STR', 'str'],
  ['CON', 'con'],
  ['DEX', 'dex'],
  ['INT', 'int'],
] as const
</script>

<template>
  <PaperCard hover tag="article" class="focus-within:border-seal/60">
    <div class="mb-2 flex items-start justify-between gap-2">
      <div class="min-w-0">
        <h3 class="font-serif font-bold text-ink">
          <!-- NOTE: 标题按钮的伪元素铺满整张卡，整卡可点；删除按钮 z-10 压在上面，避免按钮嵌套按钮 -->
          <button type="button" class="text-left after:absolute after:inset-0" @click="emit('open', card)">{{ card.name }}</button>
        </h3>
        <p class="mt-0.5 font-type text-xs text-ink-faint">{{ cardSubtitle(card) }}</p>
      </div>
      <button
        type="button"
        class="relative z-10 shrink-0 px-1 py-0.5 text-xs text-ink-faint transition hover:text-seal"
        :aria-label="`删除人物卡 ${card.name}`"
        @click="emit('delete', card)"
      >
        ✕
      </button>
    </div>

    <div class="mb-3 grid grid-cols-4 gap-1">
      <StatCell v-for="[label, key] in HEADLINE_STATS" :key="key" :label="label" :value="card.stats?.[key]" />
    </div>

    <div class="space-y-1.5">
      <StatBar label="HP" tone="moss" :value="card.stats?.hp ?? 0" :max="card.stats?.max_hp ?? 0" />
      <StatBar label="SAN" tone="seal" :value="card.stats?.san ?? 0" :max="card.stats?.max_san ?? 0" />
      <StatBar label="MP" tone="ledger" :value="card.stats?.mp ?? 0" :max="card.stats?.max_mp ?? 0" />
    </div>

    <div v-if="hasMadness(card)" class="mt-2">
      <StampBadge tone="seal">{{ madnessLabel(card.madness_state) }}</StampBadge>
    </div>
  </PaperCard>
</template>
