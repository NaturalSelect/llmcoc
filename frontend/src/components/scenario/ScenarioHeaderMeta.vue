<script setup lang="ts">
import { computed } from 'vue'
import type { Scenario } from '@/api/scenarioTypes'
import { difficultyLabel } from '@/utils/format'
import ScenarioChip from './ScenarioChip.vue'
import { playersLabel, splitTags } from './scenarioFormat'

const props = defineProps<{ scenario: Scenario }>()
const tags = computed(() => splitTags(props.scenario.tags))
</script>

<template>
  <div class="mb-5 flex flex-wrap items-center gap-2">
    <ScenarioChip tone="ledger">{{ difficultyLabel(scenario.difficulty) }}</ScenarioChip>
    <span class="text-sm tabular-nums text-fg-muted">{{ playersLabel(scenario.min_players, scenario.max_players) }}</span>
    <span v-if="scenario.author" class="text-sm text-fg-subtle">作者: {{ scenario.author }}</span>
    <ScenarioChip v-for="tag in tags" :key="tag">{{ tag }}</ScenarioChip>
  </div>
</template>
