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
    class="w-full rounded-sm border border-paper-edge bg-paper-deep/60 px-3 py-2 font-serif text-ink outline-none transition placeholder:text-ink-faint focus:border-seal disabled:cursor-not-allowed disabled:opacity-60"
  />
</template>
