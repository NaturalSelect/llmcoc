<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    label: string
    value: number
    max: number
    tone?: 'hp' | 'san' | 'mp'
    flash?: boolean
  }>(),
  { tone: 'hp', flash: false },
)

// NOTE: 与旧前端 statPercent 一致：max 非正返回 0，结果夹在 0 到 100 之间
const percent = computed(() => {
  const total = Number(props.max) || 0
  if (total <= 0) return 0
  return Math.max(0, Math.min(100, Math.round(((Number(props.value) || 0) / total) * 100)))
})
</script>

<template>
  <div class="flex items-center gap-2 rounded-md text-xs" :class="flash ? 'stat-flash' : ''">
    <span class="w-8 shrink-0 text-fg-muted">{{ label }}</span>
    <div
      class="h-2 flex-1 overflow-hidden rounded-full bg-surface-3"
      role="progressbar"
      :aria-valuenow="value"
      :aria-valuemax="max"
      :aria-label="label"
    >
      <div
        class="h-full rounded-full transition-all"
        :class="{ 'bg-hp': tone === 'hp', 'bg-san': tone === 'san', 'bg-mp': tone === 'mp' }"
        :style="{ width: percent + '%' }"
      />
    </div>
    <span class="w-14 shrink-0 text-right tabular-nums text-fg">{{ value }}/{{ max }}</span>
  </div>
</template>

<style scoped>
.stat-flash {
  animation: statFlash 0.7s ease-out;
}
@keyframes statFlash {
  0%,
  60% {
    background: color-mix(in srgb, var(--color-accent) 25%, transparent);
  }
  100% {
    background: transparent;
  }
}
</style>
