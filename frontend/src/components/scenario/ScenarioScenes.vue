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
      <li v-for="(sc, i) in scenes" :key="`${sc.id ?? ''}-${i}`" class="flex items-start gap-3 rounded-lg bg-surface-2 p-3">
        <span class="mt-0.5 w-5 shrink-0 text-xs font-medium tabular-nums text-fg-subtle">{{ i + 1 }}.</span>
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-1.5">
            <span class="text-sm font-semibold text-fg">{{ sc.name }}</span>
            <ScenarioChip v-if="sc.id" class="font-mono">ID: {{ sc.id }}</ScenarioChip>
          </div>
          <p v-if="sc.description" class="mt-1 text-sm leading-relaxed text-fg-muted">{{ sc.description }}</p>
          <div v-if="sc.triggers?.length" class="mt-2 flex flex-wrap gap-1.5">
            <ScenarioChip v-for="(tr, ti) in sc.triggers" :key="`${tr}-${ti}`">{{ tr }}</ScenarioChip>
          </div>
        </div>
      </li>
    </ol>
  </ScenarioSection>
</template>
