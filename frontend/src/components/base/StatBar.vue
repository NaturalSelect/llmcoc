<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    label: string
    value: number
    max: number
    tone?: 'seal' | 'ledger' | 'moss'
    flash?: boolean
  }>(),
  { tone: 'seal', flash: false },
)

// NOTE: 与旧前端 statPercent 一致：max 非正返回 0，结果夹在 0 到 100 之间
const percent = computed(() => {
  const total = Number(props.max) || 0
  if (total <= 0) return 0
  return Math.max(0, Math.min(100, Math.round(((Number(props.value) || 0) / total) * 100)))
})
</script>

<template>
  <div class="flex items-center gap-2 font-type text-xs" :class="flash ? 'stat-flash' : ''">
    <span class="w-8 shrink-0 text-ink-soft">{{ label }}</span>
    <div
      class="h-1.5 flex-1 overflow-hidden rounded-full bg-paper-edge"
      role="progressbar"
      :aria-valuenow="value"
      :aria-valuemax="max"
      :aria-label="label"
    >
      <div
        class="h-full rounded-full transition-all"
        :class="{ 'bg-seal': tone === 'seal', 'bg-ledger': tone === 'ledger', 'bg-moss': tone === 'moss' }"
        :style="{ width: percent + '%' }"
      />
    </div>
    <span class="w-14 shrink-0 text-right text-ink">{{ value }}/{{ max }}</span>
  </div>
</template>

<style scoped>
.stat-flash {
  animation: statFlash 0.7s ease-out;
}
@keyframes statFlash {
  0%,
  60% {
    background: color-mix(in srgb, var(--color-seal) 25%, transparent);
  }
  100% {
    background: transparent;
  }
}
</style>
