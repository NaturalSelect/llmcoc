<script setup lang="ts">
import { computed } from 'vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import type { ScenarioKeeperAppendix } from '@/api/sessionTypes'
import { keeperAppendixRows } from './scenarioFormat'

const props = defineProps<{ appendix: ScenarioKeeperAppendix }>()
const rows = computed(() => keeperAppendixRows(props.appendix))
</script>

<template>
  <section class="mb-6 rounded-lg bg-surface-2 p-4" aria-label="守秘人附录">
    <SectionHeading class="mb-2">守秘人附录</SectionHeading>
    <div class="space-y-1.5">
      <div v-for="r in rows" :key="r.key" class="text-sm leading-relaxed text-fg-muted" :class="r.multiline ? 'whitespace-pre-wrap' : ''">
        <span class="text-fg-subtle">{{ r.label }}</span><span>{{ r.text }}</span>
      </div>
    </div>
  </section>
</template>
