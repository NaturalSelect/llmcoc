<script setup lang="ts">
defineProps<{ title: string; note?: string }>()
</script>

<template>
  <section class="mb-5">
    <h3 class="mb-2 flex items-baseline gap-1.5 font-type text-xs font-bold tracking-wide text-seal">
      <span aria-hidden="true">▍</span>{{ title }}
      <span v-if="note" class="text-[10px] font-normal text-ink-faint">{{ note }}</span>
    </h3>
    <slot />
  </section>
</template>
