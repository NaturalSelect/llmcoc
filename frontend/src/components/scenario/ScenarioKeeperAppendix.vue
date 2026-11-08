<script setup lang="ts">
import { computed } from 'vue'
import type { ScenarioKeeperAppendix } from '@/api/sessionTypes'
import { keeperAppendixRows } from './scenarioFormat'

const props = defineProps<{ appendix: ScenarioKeeperAppendix }>()
const rows = computed(() => keeperAppendixRows(props.appendix))
</script>

<template>
  <section class="mb-5 rounded-sm border border-paper-edge bg-paper-deep/50 p-3" aria-label="守秘人附录">
    <div class="mb-1.5 font-type text-[10px] font-bold uppercase tracking-wider text-ink-soft">守秘人附录</div>
    <div class="space-y-1">
      <div
        v-for="r in rows"
        :key="r.key"
        class="font-serif text-[11px] leading-normal text-ink-soft"
        :class="r.multiline ? 'whitespace-pre-wrap' : ''"
      >
        <span class="text-ink-faint">{{ r.label }}</span><span>{{ r.text }}</span>
      </div>
    </div>
  </section>
</template>
