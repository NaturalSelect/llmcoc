<script setup lang="ts">
import type { Tone } from './scenarioFormat'

withDefaults(defineProps<{ tone?: Tone }>(), { tone: 'faint' })
</script>

<template>
  <span
    class="inline-block rounded-full px-2 py-0.5 text-xs leading-tight"
    :class="{
      'bg-accent/15 text-accent': tone === 'seal',
      'bg-info/15 text-info': tone === 'ledger',
      'bg-success/15 text-success': tone === 'moss',
      'bg-surface-3 text-fg-muted': tone === 'faint',
    }"
  >
    <slot />
  </span>
</template>
