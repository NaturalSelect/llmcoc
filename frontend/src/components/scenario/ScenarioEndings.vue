<script setup lang="ts">
import type { ScenarioEnding } from '@/api/sessionTypes'
import ScenarioChip from './ScenarioChip.vue'
import ScenarioSection from './ScenarioSection.vue'
import { countLabel } from './scenarioFormat'

defineProps<{ endings: ScenarioEnding[] }>()
</script>

<template>
  <ScenarioSection title="结局" :note="countLabel(endings.length, '个')">
    <ul class="space-y-2">
      <li
        v-for="(ed, i) in endings"
        :key="i"
        class="rounded-sm border p-2.5"
        :class="ed.is_failure ? 'border-seal/40 bg-seal/5' : 'border-paper-edge bg-paper-deep/50'"
      >
        <div class="mb-1 flex items-center justify-between gap-2">
          <span class="font-serif text-sm font-bold" :class="ed.is_failure ? 'text-seal' : 'text-ink'">{{ ed.name }}</span>
          <ScenarioChip v-if="ed.san_reward" class="shrink-0">SAN: {{ ed.san_reward }}</ScenarioChip>
        </div>
        <p v-if="ed.trigger" class="font-serif text-xs leading-relaxed text-ink">{{ ed.trigger }}</p>
        <p v-if="ed.description" class="mt-1 font-serif text-[11px] leading-normal text-ink-soft">{{ ed.description }}</p>
      </li>
    </ul>
  </ScenarioSection>
</template>
