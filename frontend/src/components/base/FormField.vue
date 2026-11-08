<script setup lang="ts">
defineProps<{ label?: string; hint?: string }>()
</script>

<template>
  <label class="block">
    <span v-if="label" class="mb-1 block font-type text-xs tracking-wide text-ink-soft">{{ label }}</span>
    <slot />
    <span v-if="hint" class="mt-1 block text-[11px] text-ink-faint">{{ hint }}</span>
  </label>
</template>
