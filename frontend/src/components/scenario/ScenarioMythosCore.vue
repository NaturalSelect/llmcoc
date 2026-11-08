<script setup lang="ts">
import SectionHeading from '@/components/base/SectionHeading.vue'

defineProps<{ text: string }>()
</script>

<template>
  <section class="mb-6 rounded-lg bg-accent/10 p-4" aria-label="神话本质">
    <SectionHeading class="mb-1.5">神话本质（永远隐藏）</SectionHeading>
    <div class="whitespace-pre-wrap text-sm leading-relaxed text-fg">{{ text }}</div>
  </section>
</template>
