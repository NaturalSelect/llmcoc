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
    class="w-full resize-y rounded-sm border border-paper-edge bg-paper-deep/60 px-3 py-2 font-serif leading-relaxed text-ink outline-none transition placeholder:text-ink-faint focus:border-seal disabled:cursor-not-allowed disabled:opacity-60"
  />
</template>
