<script setup lang="ts">
import type { ScenarioTimelineEvent } from '@/api/sessionTypes'
import ScenarioChip from './ScenarioChip.vue'
import ScenarioSection from './ScenarioSection.vue'
import { phaseLabel } from './scenarioFormat'

defineProps<{ timeline: ScenarioTimelineEvent[] }>()
</script>

<template>
  <ScenarioSection title="时间线">
    <ol class="space-y-1.5">
      <li v-for="(tl, i) in timeline" :key="i" class="flex items-start gap-2 rounded-sm border border-paper-edge bg-paper-deep/50 p-2">
        <ScenarioChip class="shrink-0">{{ tl.time }}</ScenarioChip>
        <span class="flex-1 font-serif text-xs leading-normal text-ink">{{ tl.event }}</span>
        <span v-if="tl.phase" class="shrink-0 font-type text-[10px] text-ink-faint">{{ phaseLabel(tl.phase) }}</span>
      </li>
    </ol>
  </ScenarioSection>
</template>
