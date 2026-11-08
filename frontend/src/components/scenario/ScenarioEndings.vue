<script setup lang="ts">
import type { ScenarioEnding } from '@/api/sessionTypes'
import ScenarioChip from './ScenarioChip.vue'
import ScenarioSection from './ScenarioSection.vue'
import { countLabel } from './scenarioFormat'

defineProps<{ endings: ScenarioEnding[] }>()
</script>

<template>
  <ScenarioSection title="结局" :note="countLabel(endings.length, '个')">
    <ul class="space-y-4">
      <li v-for="(ed, i) in endings" :key="i">
        <div class="flex items-center justify-between gap-2">
          <span class="text-sm font-semibold" :class="ed.is_failure ? 'text-accent' : 'text-fg'">{{ ed.name }}</span>
          <ScenarioChip v-if="ed.san_reward" class="shrink-0">SAN: {{ ed.san_reward }}</ScenarioChip>
        </div>
        <p v-if="ed.trigger" class="mt-1 text-sm leading-relaxed text-fg">{{ ed.trigger }}</p>
        <p v-if="ed.description" class="mt-1 text-sm leading-relaxed text-fg-muted">{{ ed.description }}</p>
      </li>
    </ul>
  </ScenarioSection>
</template>
