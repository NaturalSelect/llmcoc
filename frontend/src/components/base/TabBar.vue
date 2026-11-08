<script setup lang="ts">
defineProps<{ tabs: Array<{ key: string; label: string }> }>()
const model = defineModel<string>({ required: true })
</script>

<template>
  <div class="flex gap-1 overflow-x-auto border-b border-paper-edge" role="tablist">
    <button
      v-for="t in tabs"
      :key="t.key"
      type="button"
      role="tab"
      :aria-selected="model === t.key"
      class="shrink-0 border-b-2 px-3 py-2 font-serif text-sm transition"
      :class="model === t.key ? 'border-seal text-seal' : 'border-transparent text-ink-soft hover:text-ink'"
      @click="model = t.key"
    >
      {{ t.label }}
    </button>
  </div>
</template>
