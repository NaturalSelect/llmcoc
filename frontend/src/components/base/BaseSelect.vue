<script setup lang="ts">
// NOTE: option 的 :value 保留原始类型（数字不会变成字符串），所以这里不需要 .number 修饰符
const model = defineModel<unknown>()
</script>

<template>
  <select
    v-model="model"
    class="w-full rounded-sm border border-paper-edge bg-paper-deep/60 px-3 py-2 font-serif text-ink outline-none transition focus:border-seal disabled:cursor-not-allowed disabled:opacity-60"
  >
    <slot />
  </select>
</template>
