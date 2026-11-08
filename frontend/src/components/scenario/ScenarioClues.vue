<script setup lang="ts">
import type { ScenarioClue } from '@/api/sessionTypes'
import ScenarioChip from './ScenarioChip.vue'
import ScenarioSection from './ScenarioSection.vue'
import { clueNatureLabel, clueNatureTone, countLabel } from './scenarioFormat'

defineProps<{ clues: ScenarioClue[] }>()
</script>

<template>
  <ScenarioSection title="线索" :note="countLabel(clues.length, '条')">
    <ol class="space-y-2">
      <li v-for="(cl, i) in clues" :key="i" class="flex items-start gap-3 rounded-lg bg-surface-2 p-3">
        <span class="mt-0.5 w-5 shrink-0 text-xs font-medium tabular-nums text-fg-subtle">{{ i + 1 }}</span>
        <div class="min-w-0 flex-1">
          <div class="mb-1 flex flex-wrap items-center gap-1.5">
            <ScenarioChip :tone="clueNatureTone(cl.nature)" class="shrink-0">{{ clueNatureLabel(cl.nature) }}</ScenarioChip>
            <span v-if="cl.source" class="text-xs text-fg-subtle">{{ cl.source }}</span>
            <ScenarioChip v-if="cl.skill_check">检定: {{ cl.skill_check }}</ScenarioChip>
          </div>
          <div class="text-sm leading-relaxed text-fg">{{ cl.summary }}</div>
          <div v-if="cl.on_success" class="mt-1 text-sm leading-relaxed text-fg-muted">
            <span class="text-fg-subtle">成功：</span><span>{{ cl.on_success }}</span>
          </div>
          <div v-if="cl.on_failure" class="mt-1 text-sm leading-relaxed text-fg-muted">
            <span class="text-fg-subtle">失败推进：</span><span>{{ cl.on_failure }}</span>
          </div>
        </div>
      </li>
    </ol>
  </ScenarioSection>
</template>
