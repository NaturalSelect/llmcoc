<script setup lang="ts">
import SectionHeading from '@/components/base/SectionHeading.vue'
import type { ScenarioRewardInfo } from '@/api/sessionTypes'
import ScenarioChip from './ScenarioChip.vue'

defineProps<{ reward: ScenarioRewardInfo }>()
</script>

<template>
  <section class="mb-6 rounded-lg bg-surface-2 p-4" aria-label="奖励">
    <div class="mb-2 flex items-start justify-between gap-2">
      <div>
        <SectionHeading class="mb-1">奖励</SectionHeading>
        <div class="text-sm font-semibold text-fg">{{ reward.name || '未命名奖励' }}</div>
      </div>
      <ScenarioChip v-if="reward.type" class="shrink-0">{{ reward.type }}</ScenarioChip>
    </div>
    <p v-if="reward.description" class="mb-2 text-sm leading-relaxed text-fg">{{ reward.description }}</p>
    <div class="space-y-1.5">
      <div v-if="reward.mechanics_note" class="text-sm leading-relaxed text-fg-muted">
        <span class="text-fg-subtle">机制说明：</span><span>{{ reward.mechanics_note }}</span>
      </div>
      <div v-if="reward.find_condition" class="text-sm leading-relaxed text-fg-muted">
        <span class="text-fg-subtle">获得条件：</span><span>{{ reward.find_condition }}</span>
      </div>
    </div>
  </section>
</template>
