<script setup lang="ts">
import SectionHeading from '@/components/base/SectionHeading.vue'

defineProps<{ title: string }>()
</script>

<template>
  <section class="mb-5">
    <SectionHeading class="mb-2">{{ title }}</SectionHeading>
    <slot />
  </section>
</template>
