<script setup lang="ts">
defineProps<{ count?: number }>()
</script>

<template>
  <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
    <slot />
    <span v-if="count != null" class="rounded-full bg-surface-3 px-2 py-0.5 text-xs font-normal tabular-nums text-fg-muted">{{ count }}</span>
  </h2>
</template>
