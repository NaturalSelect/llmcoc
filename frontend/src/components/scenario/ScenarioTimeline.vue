<script setup lang="ts">
import type { ScenarioTimelineEvent } from '@/api/sessionTypes'
import ScenarioSection from './ScenarioSection.vue'
import { phaseLabel } from './scenarioFormat'

defineProps<{ timeline: ScenarioTimelineEvent[] }>()
</script>

<template>
  <ScenarioSection title="时间线">
    <ol class="space-y-2.5">
      <li v-for="(tl, i) in timeline" :key="i" class="flex items-baseline gap-3">
        <span class="min-w-14 shrink-0 text-sm font-medium tabular-nums text-accent">{{ tl.time }}</span>
        <span class="flex-1 text-sm leading-relaxed text-fg">{{ tl.event }}</span>
        <span v-if="tl.phase" class="shrink-0 text-xs text-fg-subtle">{{ phaseLabel(tl.phase) }}</span>
      </li>
    </ol>
  </ScenarioSection>
</template>
