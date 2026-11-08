<script setup lang="ts">
const [model, modifiers] = defineModel<string | null | undefined>({
  set(v) {
    return modifiers.trim && typeof v === 'string' ? v.trim() : v
  },
})
</script>

<template>
  <textarea
    v-model="model"
    class="min-h-20 w-full resize-y rounded-lg border border-line bg-surface-2 px-3 py-2 text-sm leading-relaxed text-fg outline-none transition placeholder:text-fg-subtle focus:border-accent focus:ring-2 focus:ring-accent/30 disabled:cursor-not-allowed disabled:opacity-60"
  />
</template>
