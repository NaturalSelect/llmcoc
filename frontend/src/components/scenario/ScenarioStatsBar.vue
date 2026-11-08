<script setup lang="ts">
import { computed } from 'vue'
import type { ScenarioContentView } from '@/api/sessionTypes'
import { listLength, slotToTime } from './scenarioFormat'

const props = defineProps<{ content: ScenarioContentView }>()

const stats = computed(() => [
  { label: '开局时间', value: slotToTime(props.content.game_start_slot) },
  { label: '场景数', value: listLength(props.content.scenes) },
  { label: 'NPC 数', value: listLength(props.content.npcs) },
  { label: '线索数', value: listLength(props.content.clues) },
])
</script>

<template>
  <dl class="mb-6 grid grid-cols-2 gap-2 sm:grid-cols-4">
    <div v-for="s in stats" :key="s.label" class="rounded-lg bg-surface-2 px-3 py-2.5 text-center">
      <dt class="mb-0.5 text-xs text-fg-subtle">{{ s.label }}</dt>
      <dd class="text-sm font-semibold tabular-nums text-fg">{{ s.value }}</dd>
    </div>
  </dl>
</template>
