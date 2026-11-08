<script setup lang="ts">
defineProps<{ label: string; value?: string | number | null; big?: boolean }>()
</script>

<template>
  <div class="rounded-sm border border-paper-edge bg-paper-deep/50 px-1 py-1.5 text-center">
    <div class="font-type text-[11px] text-ink-faint">{{ label }}</div>
    <div class="font-type font-bold text-ink" :class="big ? 'text-xl' : 'text-sm'">{{ value }}</div>
  </div>
</template>
