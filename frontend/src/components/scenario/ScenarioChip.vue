<script setup lang="ts">
import type { Tone } from './scenarioFormat'

withDefaults(defineProps<{ tone?: Tone }>(), { tone: 'faint' })
</script>

<template>
  <span
    class="inline-block rounded-sm border px-1.5 py-0.5 font-type text-[10px] leading-tight"
    :class="{
      'border-seal/60 bg-seal/5 text-seal': tone === 'seal',
      'border-ledger/60 bg-ledger/5 text-ledger': tone === 'ledger',
      'border-moss/60 bg-moss/5 text-moss': tone === 'moss',
      'border-paper-edge bg-paper-deep/60 text-ink-soft': tone === 'faint',
    }"
  >
    <slot />
  </span>
</template>
