<script setup lang="ts">
import type { CharacterCardFull } from '@/api/characterTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { deadSubtitle } from './characterLogic'

defineProps<{ card: CharacterCardFull; cost: number; coins: number; reviving: boolean }>()
const emit = defineEmits<{ open: [card: CharacterCardFull]; delete: [card: CharacterCardFull]; revive: [card: CharacterCardFull] }>()
</script>

<template>
  <PaperCard tag="article" class="border-seal/40!">
    <div class="mb-2 flex items-start justify-between gap-2">
      <div class="min-w-0 flex-1">
        <h3 class="font-serif font-bold">
          <button type="button" class="text-left text-ink-soft transition hover:text-seal" @click="emit('open', card)">{{ card.name }}</button>
        </h3>
        <p class="mt-0.5 font-type text-xs text-ink-faint">{{ deadSubtitle(card) }}</p>
      </div>
      <StampBadge tone="seal" class="shrink-0">已阵亡</StampBadge>
    </div>
    <div class="mt-3 flex flex-wrap items-center justify-between gap-2">
      <span class="font-type text-xs text-ink-soft">复活费用：<b class="text-seal">{{ cost }}</b> 金币</span>
      <div class="flex gap-2">
        <BaseButton variant="danger" class="px-2! py-1! text-xs!" @click="emit('delete', card)">🗑 彻底删除</BaseButton>
        <BaseButton class="px-2! py-1! text-xs!" :disabled="coins < cost || reviving" @click="emit('revive', card)">✨ 复活</BaseButton>
      </div>
    </div>
  </PaperCard>
</template>
