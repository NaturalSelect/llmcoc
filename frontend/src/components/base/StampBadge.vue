<script setup lang="ts">
withDefaults(defineProps<{ tone?: 'seal' | 'ledger' | 'moss' | 'faint' }>(), { tone: 'faint' })
</script>

<template>
  <span
    class="inline-block -rotate-2 rounded-sm border px-1.5 py-0.5 font-type text-[11px] tracking-widest"
    :class="{
      'border-seal text-seal': tone === 'seal',
      'border-ledger text-ledger': tone === 'ledger',
      'border-moss text-moss': tone === 'moss',
      'border-ink-faint text-ink-faint': tone === 'faint',
    }"
  >
    <slot />
  </span>
</template>
