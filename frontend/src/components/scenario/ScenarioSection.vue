<script setup lang="ts">
import SectionHeading from '@/components/base/SectionHeading.vue'

defineProps<{ title: string; note?: string }>()
</script>

<template>
  <section class="mb-6">
    <div class="mb-2.5 flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
      <SectionHeading>{{ title }}</SectionHeading>
      <span v-if="note" class="text-xs text-fg-subtle">{{ note }}</span>
    </div>
    <slot />
  </section>
</template>
