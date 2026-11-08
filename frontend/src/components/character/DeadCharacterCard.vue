<script setup lang="ts">
import { Coins, Skull, Sparkles, Trash2 } from '@lucide/vue'
import type { CharacterCardFull } from '@/api/characterTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { deadSubtitle } from './characterLogic'

defineProps<{ card: CharacterCardFull; cost: number; coins: number; reviving: boolean }>()
const emit = defineEmits<{ open: [card: CharacterCardFull]; delete: [card: CharacterCardFull]; revive: [card: CharacterCardFull] }>()
</script>

<template>
  <PaperCard tag="article">
    <div class="flex items-start gap-3">
      <div class="flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-surface-3 text-fg-subtle" aria-hidden="true">
        <Skull :size="16" :stroke-width="1.75" />
      </div>
      <div class="min-w-0 flex-1">
        <h3 class="truncate text-sm font-semibold">
          <button type="button" class="max-w-full truncate text-left text-fg-muted transition hover:text-fg" @click="emit('open', card)">{{ card.name }}</button>
        </h3>
        <p class="mt-0.5 truncate text-xs text-fg-subtle">{{ deadSubtitle(card) }}</p>
      </div>
      <StampBadge tone="danger" class="shrink-0">已阵亡</StampBadge>
    </div>
    <div class="mt-4 flex flex-wrap items-center justify-between gap-3">
      <span class="inline-flex items-center gap-1.5 text-xs text-fg-muted">
        <Coins :size="16" :stroke-width="1.75" class="text-accent" />
        <span>复活费用：<b class="font-semibold tabular-nums text-accent">{{ cost }}</b> 金币</span>
      </span>
      <div class="flex gap-2">
        <BaseButton variant="danger" size="sm" @click="emit('delete', card)">
          <Trash2 :size="16" :stroke-width="1.75" />
          彻底删除
        </BaseButton>
        <BaseButton variant="success" size="sm" :disabled="coins < cost || reviving" @click="emit('revive', card)">
          <Sparkles :size="16" :stroke-width="1.75" />
          复活
        </BaseButton>
      </div>
    </div>
  </PaperCard>
</template>
