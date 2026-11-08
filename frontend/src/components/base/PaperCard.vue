<script setup lang="ts">
withDefaults(defineProps<{ hover?: boolean; tag?: string }>(), { hover: false, tag: 'div' })
</script>

<template>
  <component
    :is="tag"
    class="relative rounded-sm border border-paper-edge bg-paper p-4 shadow-[0_1px_0_rgb(0_0_0/0.04),0_6px_14px_-8px_rgb(60_40_20/0.35)]"
    :class="hover ? 'transition hover:-translate-y-0.5 hover:border-seal/60 hover:shadow-md' : ''"
  >
    <slot />
  </component>
</template>
