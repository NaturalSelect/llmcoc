<script setup lang="ts">
defineProps<{ label?: string; hint?: string }>()
</script>

<template>
  <label class="block">
    <span v-if="label" class="mb-1.5 block text-sm text-fg-muted">{{ label }}</span>
    <slot />
    <span v-if="hint" class="mt-1 block text-xs text-fg-subtle">{{ hint }}</span>
  </label>
</template>
