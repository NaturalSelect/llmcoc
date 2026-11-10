<script setup lang="ts">
withDefaults(defineProps<{ items: string[]; tone?: 'ink' | 'ledger' | 'seal' }>(), { tone: 'ink' })
</script>

<template>
  <ul v-if="items.length" class="flex flex-wrap gap-2">
    <li
      v-for="(item, idx) in items"
      :key="idx"
      class="inline-flex items-center gap-1.5 rounded-full bg-surface-3 px-2.5 py-1 text-xs"
      :class="{
        'text-fg': tone === 'ink',
        'text-info': tone === 'ledger',
        'text-accent': tone === 'seal',
      }"
    >
      <slot name="label" :item="item" :index="idx"><span>{{ item }}</span></slot>
      <slot name="actions" :item="item" :index="idx" />
    </li>
  </ul>
  <p v-else class="text-xs text-fg-subtle">—</p>
</template>
