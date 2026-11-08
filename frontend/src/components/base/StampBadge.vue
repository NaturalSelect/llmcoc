<script setup lang="ts">
withDefaults(defineProps<{ tone?: 'seal' | 'ledger' | 'moss' | 'faint' | 'danger' }>(), { tone: 'faint' })
</script>

<template>
  <span
    class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium"
    :class="{
      'bg-accent/15 text-accent': tone === 'seal',
      'bg-info/15 text-info': tone === 'ledger',
      'bg-success/15 text-success': tone === 'moss',
      'bg-danger/15 text-danger': tone === 'danger',
      'bg-surface-3 text-fg-muted': tone === 'faint',
    }"
  >
    <slot />
  </span>
</template>
