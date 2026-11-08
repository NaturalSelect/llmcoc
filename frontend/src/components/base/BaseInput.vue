<script setup lang="ts">
const [model, modifiers] = defineModel<string | number | null | undefined>({
  set(v) {
    if (modifiers.number && typeof v === 'string') {
      const n = Number(v)
      return v === '' || Number.isNaN(n) ? v : n
    }
    if (modifiers.trim && typeof v === 'string') return v.trim()
    return v
  },
})
</script>

<template>
  <input
    v-model="model"
    class="h-10 w-full rounded-lg border border-line bg-surface-2 px-3 text-sm text-fg outline-none transition placeholder:text-fg-subtle focus:border-accent focus:ring-2 focus:ring-accent/30 disabled:cursor-not-allowed disabled:opacity-60"
  />
</template>
