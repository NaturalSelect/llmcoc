<script setup lang="ts">
defineProps<{ title: string }>()
</script>

<template>
  <section class="mb-4">
    <h3 class="mb-2 font-type text-xs font-bold uppercase tracking-wider text-ink-soft">{{ title }}</h3>
    <slot />
  </section>
</template>
