<script setup lang="ts">
import type { ScenarioClue } from '@/api/sessionTypes'
import ScenarioChip from './ScenarioChip.vue'
import ScenarioSection from './ScenarioSection.vue'
import { clueNatureLabel, clueNatureTone, countLabel } from './scenarioFormat'

defineProps<{ clues: ScenarioClue[] }>()
</script>

<template>
  <ScenarioSection title="线索" :note="countLabel(clues.length, '条')">
    <ol class="space-y-1.5">
      <li v-for="(cl, i) in clues" :key="i" class="flex items-start gap-2 rounded-sm border border-paper-edge bg-paper-deep/50 p-2">
        <span class="mt-0.5 shrink-0 font-type text-[10px] font-bold text-ink-faint">{{ i + 1 }}</span>
        <div class="min-w-0 flex-1">
          <div class="mb-0.5 flex flex-wrap items-center gap-1.5">
            <ScenarioChip :tone="clueNatureTone(cl.nature)" class="shrink-0">{{ clueNatureLabel(cl.nature) }}</ScenarioChip>
            <span v-if="cl.source" class="font-type text-[10px] text-ink-faint">{{ cl.source }}</span>
            <ScenarioChip v-if="cl.skill_check">检定: {{ cl.skill_check }}</ScenarioChip>
          </div>
          <div class="font-serif text-xs leading-normal text-ink">{{ cl.summary }}</div>
          <div v-if="cl.on_success" class="mt-0.5 font-serif text-[11px] leading-normal text-ink-soft">
            <span class="text-ink-faint">成功：</span><span>{{ cl.on_success }}</span>
          </div>
          <div v-if="cl.on_failure" class="mt-0.5 font-serif text-[11px] leading-normal text-ink-soft">
            <span class="text-ink-faint">失败推进：</span><span>{{ cl.on_failure }}</span>
          </div>
        </div>
      </li>
    </ol>
  </ScenarioSection>
</template>
