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
      <li v-for="(mc, i) in mechanics" :key="i" class="rounded-sm border border-paper-edge bg-paper-deep/50 p-2.5">
        <div class="mb-1 flex items-center gap-1.5">
          <span class="font-serif text-sm font-bold text-ink">{{ mc.name }}</span>
          <ScenarioChip>{{ mc.type }}</ScenarioChip>
        </div>
        <p v-if="mc.description" class="font-serif text-[11px] leading-normal text-ink-soft">{{ mc.description }}</p>
        <div v-if="mc.stages?.length" class="mt-1.5 space-y-1">
          <div v-for="(st, si) in mc.stages" :key="si" class="font-serif text-[11px] leading-normal text-ink-soft">
            <span class="text-ink-faint">{{ mechanicStageLabel(st) }}</span><span>{{ mechanicStageText(st) }}</span>
          </div>
        </div>
      </li>
    </ul>
  </ScenarioSection>
</template>
