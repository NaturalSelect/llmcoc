<script setup lang="ts">
import type { ScenarioMechanic } from '@/api/sessionTypes'
import ScenarioChip from './ScenarioChip.vue'
import ScenarioSection from './ScenarioSection.vue'
import { mechanicStageLabel, mechanicStageText } from './scenarioFormat'

defineProps<{ mechanics: ScenarioMechanic[] }>()
</script>

<template>
  <ScenarioSection title="量化机制" note="（KP参考，不做自动结算）">
    <ul class="space-y-2">
      <li v-for="(mc, i) in mechanics" :key="i" class="rounded-lg bg-surface-2 p-3">
        <div class="mb-1 flex items-center gap-2">
          <span class="text-sm font-semibold text-fg">{{ mc.name }}</span>
          <ScenarioChip>{{ mc.type }}</ScenarioChip>
        </div>
        <p v-if="mc.description" class="text-sm leading-relaxed text-fg-muted">{{ mc.description }}</p>
        <div v-if="mc.stages?.length" class="mt-2 space-y-1">
          <div v-for="(st, si) in mc.stages" :key="si" class="text-sm leading-relaxed text-fg-muted">
            <span class="text-fg-subtle">{{ mechanicStageLabel(st) }}</span><span>{{ mechanicStageText(st) }}</span>
          </div>
        </div>
      </li>
    </ul>
  </ScenarioSection>
</template>
