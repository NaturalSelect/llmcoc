<script setup lang="ts">
// NOTE: option 的 :value 保留原始类型（数字不会变成字符串），所以这里不需要 .number 修饰符
const model = defineModel<unknown>()
</script>

<template>
  <select
    v-model="model"
    class="h-10 w-full rounded-lg border border-line bg-surface-2 px-3 text-sm text-fg outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/30 disabled:cursor-not-allowed disabled:opacity-60"
  >
    <slot />
  </select>
</template>
