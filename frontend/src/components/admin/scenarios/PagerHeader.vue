<script setup lang="ts">
import BaseButton from '@/components/base/BaseButton.vue'

// NOTE: 四个页签的分页列表头共用：标题 + 「共 N 个 · 第 p / t 页」+ 上一页/下一页/刷新，页签各自的按钮走 before/after 插槽
defineProps<{ title: string; unit: string; note?: string; page: number; totalPages: number; total: number }>()
defineEmits<{ prev: []; next: []; refresh: [] }>()
</script>

<template>
  <div class="mb-4 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
    <div>
      <h3 class="font-type text-sm font-bold tracking-wide text-seal">{{ title }}</h3>
      <p class="mt-1 text-xs text-ink-faint">{{ note ? note + ' ' : '' }}共 {{ total }} {{ unit }} · 第 {{ page }} / {{ totalPages }} 页</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <slot name="before" />
      <BaseButton variant="ghost" class="!px-2.5 !py-1 !text-xs" :disabled="page <= 1" @click="$emit('prev')">上一页</BaseButton>
      <BaseButton variant="ghost" class="!px-2.5 !py-1 !text-xs" :disabled="page >= totalPages" @click="$emit('next')">下一页</BaseButton>
      <BaseButton variant="ghost" class="!px-2.5 !py-1 !text-xs" @click="$emit('refresh')">刷新</BaseButton>
      <slot name="after" />
    </div>
  </div>
</template>
