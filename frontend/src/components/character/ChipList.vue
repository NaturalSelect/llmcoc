<script setup lang="ts">
withDefaults(defineProps<{ items: string[]; tone?: 'ink' | 'ledger' | 'seal' }>(), { tone: 'ink' })
</script>

<template>
  <ul v-if="items.length" class="flex flex-wrap gap-1.5">
    <li
      v-for="(item, idx) in items"
      :key="idx"
      class="inline-flex items-center gap-2 rounded-sm border px-2 py-1 font-serif text-xs"
      :class="{
        'border-paper-edge bg-paper-deep/50 text-ink': tone === 'ink',
        'border-ledger/40 bg-ledger/5 text-ledger': tone === 'ledger',
        'border-seal/40 bg-seal/5 text-seal': tone === 'seal',
      }"
    >
      <span>{{ item }}</span>
      <slot name="actions" :item="item" :index="idx" />
    </li>
  </ul>
  <p v-else class="font-type text-xs text-ink-faint">—</p>
</template>
