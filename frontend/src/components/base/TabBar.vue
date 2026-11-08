<script setup lang="ts">
defineProps<{ tabs: Array<{ key: string; label: string }> }>()
const model = defineModel<string>({ required: true })
</script>

<template>
  <div class="flex gap-1 overflow-x-auto border-b border-line" role="tablist">
    <button
      v-for="t in tabs"
      :key="t.key"
      type="button"
      role="tab"
      :aria-selected="model === t.key"
      class="-mb-px shrink-0 border-b-2 px-3.5 py-2.5 text-sm font-medium transition"
      :class="model === t.key ? 'border-accent text-fg' : 'border-transparent text-fg-subtle hover:text-fg-muted'"
      @click="model = t.key"
    >
      {{ t.label }}
    </button>
  </div>
</template>
