<script setup lang="ts">
import { EyeOff, Timer } from '@lucide/vue'
import type { SessionClock } from '@/game/handbookTypes'

defineProps<{
  clocks: SessionClock[]
}>()

function fillClass(c: SessionClock): string {
  const ratio = c.max > 0 ? c.current / c.max : 0
  if (ratio >= 1 || ratio >= 0.75) return 'bg-danger'
  if (ratio >= 0.5) return 'bg-warning'
  return 'bg-accent'
}

function borderClass(c: SessionClock): string {
  const ratio = c.max > 0 ? c.current / c.max : 0
  if (!c.visible) return 'border-info/40 bg-info/10'
  if (ratio >= 1) return 'border-danger/60 bg-danger/10'
  if (ratio >= 0.75) return 'border-warning/50 bg-warning/10'
  return 'border-line bg-surface-2'
}

function titleClass(c: SessionClock): string {
  if (c.current >= c.max) return 'text-danger'
  if (!c.visible) return 'text-info'
  return 'text-fg'
}
</script>

<template>
  <div v-if="clocks.length" class="shrink-0 border-b border-line bg-surface px-4 py-2" aria-label="危机时钟">
    <div class="mx-auto flex max-w-3xl flex-wrap items-center gap-2">
      <div
        v-for="clk in clocks"
        :key="clk.name"
        class="inline-flex max-w-full flex-wrap items-center gap-2 rounded-lg border px-2.5 py-1.5 text-xs transition"
        :class="borderClass(clk)"
        :title="clk.description || undefined"
      >
        <span class="inline-flex items-center gap-1 font-medium" :class="titleClass(clk)">
          <EyeOff v-if="!clk.visible" :size="13" :stroke-width="1.75" class="shrink-0" />
          <Timer v-else :size="13" :stroke-width="1.75" class="shrink-0 text-accent" />
          {{ clk.name }}
        </span>
        <span v-if="!clk.visible" class="rounded-md bg-info/15 px-1.5 py-0.5 text-info">暗钟</span>
        <div class="flex items-center gap-0.5" :aria-label="`${clk.name} ${clk.current}/${clk.max}`">
          <span
            v-for="seg in clk.max"
            :key="seg"
            class="h-2 w-2.5 rounded-full transition-colors"
            :class="seg <= clk.current ? fillClass(clk) : 'bg-surface-3'"
          />
        </div>
        <span class="font-semibold tabular-nums" :class="clk.current >= clk.max ? 'text-danger' : 'text-fg-muted'">
          {{ clk.current }}/{{ clk.max }}
        </span>
        <span v-if="clk.stage" class="text-fg-subtle">· {{ clk.stage }}</span>
      </div>
    </div>
  </div>
</template>
