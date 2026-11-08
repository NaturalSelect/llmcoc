<script setup lang="ts">
import type { ScenarioRewardInfo } from '@/api/sessionTypes'
import ScenarioChip from './ScenarioChip.vue'

defineProps<{ reward: ScenarioRewardInfo }>()
</script>

<template>
  <section class="mb-5 rounded-sm border border-seal/40 bg-seal/5 p-3" aria-label="奖励">
    <div class="mb-2 flex items-start justify-between gap-2">
      <div>
        <div class="mb-1 font-type text-[10px] font-bold uppercase tracking-wider text-seal">奖励</div>
        <div class="font-serif text-sm font-bold text-ink">{{ reward.name || '未命名奖励' }}</div>
      </div>
      <ScenarioChip v-if="reward.type" class="shrink-0">{{ reward.type }}</ScenarioChip>
    </div>
    <p v-if="reward.description" class="mb-2 font-serif text-xs leading-relaxed text-ink">{{ reward.description }}</p>
    <div class="space-y-1.5">
      <div v-if="reward.mechanics_note" class="font-serif text-[11px] leading-normal text-ink-soft">
        <span class="text-ink-faint">机制说明：</span><span>{{ reward.mechanics_note }}</span>
      </div>
      <div v-if="reward.find_condition" class="font-serif text-[11px] leading-normal text-ink-soft">
        <span class="text-ink-faint">获得条件：</span><span>{{ reward.find_condition }}</span>
      </div>
    </div>
  </section>
</template>
