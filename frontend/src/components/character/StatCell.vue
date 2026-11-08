<script setup lang="ts">
defineProps<{ label: string; value?: string | number | null; big?: boolean }>()
</script>

<template>
  <div class="rounded-lg bg-surface-2 px-2 py-1.5 text-center">
    <div class="text-xs text-fg-subtle">{{ label }}</div>
    <div class="font-semibold tabular-nums text-fg" :class="big ? 'text-xl' : 'text-sm'">{{ value }}</div>
  </div>
</template>
