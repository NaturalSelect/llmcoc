<script setup lang="ts">
import { ChevronLeft, ChevronRight, RefreshCw } from '@lucide/vue'
import BaseButton from '@/components/base/BaseButton.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'

// NOTE: 四个页签的分页列表头共用：标题 + 「共 N 个 · 第 p / t 页」+ 上一页/下一页/刷新，页签各自的按钮走 before/after 插槽
defineProps<{ title: string; unit: string; note?: string; page: number; totalPages: number; total: number }>()
defineEmits<{ prev: []; next: []; refresh: [] }>()
</script>

<template>
  <div class="mb-4 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
    <div class="min-w-0">
      <SectionHeading>{{ title }}</SectionHeading>
      <p class="mt-1 text-xs tabular-nums text-fg-subtle">{{ note ? note + ' ' : '' }}共 {{ total }} {{ unit }} · 第 {{ page }} / {{ totalPages }} 页</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <slot name="before" />
      <BaseButton variant="ghost" size="sm" :disabled="page <= 1" @click="$emit('prev')">
        <ChevronLeft :size="16" :stroke-width="1.75" />
        上一页
      </BaseButton>
      <BaseButton variant="ghost" size="sm" :disabled="page >= totalPages" @click="$emit('next')">
        下一页
        <ChevronRight :size="16" :stroke-width="1.75" />
      </BaseButton>
      <BaseButton variant="ghost" size="sm" @click="$emit('refresh')">
        <RefreshCw :size="16" :stroke-width="1.75" />
        刷新
      </BaseButton>
      <slot name="after" />
    </div>
  </div>
</template>
