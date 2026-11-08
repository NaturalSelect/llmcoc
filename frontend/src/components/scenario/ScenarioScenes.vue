<script setup lang="ts">
import type { ScenarioScene } from '@/api/sessionTypes'
import ScenarioChip from './ScenarioChip.vue'
import ScenarioSection from './ScenarioSection.vue'
import { countLabel } from './scenarioFormat'

defineProps<{ scenes: ScenarioScene[] }>()
</script>

<template>
  <ScenarioSection title="场景列表" :note="countLabel(scenes.length, '个')">
    <ol class="space-y-2">
      <li v-for="(sc, i) in scenes" :key="`${sc.id ?? ''}-${i}`" class="flex items-start gap-2 rounded-sm border border-paper-edge bg-paper-deep/50 p-2.5">
        <span class="mt-0.5 shrink-0 font-type text-[10px] font-bold text-ink-faint">{{ i + 1 }}.</span>
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-1.5">
            <span class="font-serif text-sm font-bold text-ink">{{ sc.name }}</span>
            <ScenarioChip v-if="sc.id">ID: {{ sc.id }}</ScenarioChip>
          </div>
          <p v-if="sc.description" class="mt-0.5 font-serif text-[11px] leading-normal text-ink-soft">{{ sc.description }}</p>
          <div v-if="sc.triggers?.length" class="mt-1.5 flex flex-wrap gap-1">
            <ScenarioChip v-for="(tr, ti) in sc.triggers" :key="`${tr}-${ti}`">{{ tr }}</ScenarioChip>
          </div>
        </div>
      </li>
    </ol>
  </ScenarioSection>
</template>
