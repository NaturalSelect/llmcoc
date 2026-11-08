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
  <dl class="mb-5 grid grid-cols-2 gap-2 sm:grid-cols-4">
    <div v-for="s in stats" :key="s.label" class="rounded-sm border border-paper-edge bg-paper-deep/50 p-2 text-center">
      <dt class="mb-0.5 font-type text-[10px] text-ink-faint">{{ s.label }}</dt>
      <dd class="font-type text-sm font-bold text-ink">{{ s.value }}</dd>
    </div>
  </dl>
</template>
