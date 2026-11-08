<script setup lang="ts">
withDefaults(defineProps<{ hover?: boolean; tag?: string; padding?: 'none' | 'sm' | 'md' | 'lg' }>(), {
  hover: false,
  tag: 'div',
  padding: 'md',
})
</script>

<template>
  <component
    :is="tag"
    class="relative rounded-xl border border-line bg-surface shadow-panel"
    :class="[
      { 'p-3': padding === 'sm', 'p-4': padding === 'md', 'p-6': padding === 'lg' },
      hover ? 'transition hover:border-line-strong hover:bg-surface-2' : '',
    ]"
  >
    <slot />
  </component>
</template>
