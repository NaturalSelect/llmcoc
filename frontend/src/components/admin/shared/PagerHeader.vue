<script setup lang="ts">
import { ChevronLeft, ChevronRight, RefreshCw } from '@lucide/vue'
import BaseButton from '@/components/base/BaseButton.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'

// NOTE: 分页列表头共用：标题 + 「共 N 个 · 第 p / t 页」+ 上一页/下一页/刷新，各页签自己的按钮走 before/after 插槽；
// 子页签已经给出标题的列表不传 title，只显示计数行；bottomNav 表示翻页放在列表下方（PaginationBar），这里只留计数和刷新，计数行也不再重复页码
defineProps<{ title?: string; unit: string; note?: string; page: number; totalPages: number; total: number; bottomNav?: boolean }>()
defineEmits<{ prev: []; next: []; refresh: [] }>()
</script>

<template>
  <div class="mb-4 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
    <div class="min-w-0">
      <SectionHeading v-if="title">{{ title }}</SectionHeading>
      <p class="text-xs tabular-nums text-fg-subtle" :class="{ 'mt-1': title }">{{ note ? note + ' ' : '' }}共 {{ total }} {{ unit }}{{ bottomNav ? '' : ` · 第 ${page} / ${totalPages} 页` }}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <slot name="before" />
      <BaseButton v-if="!bottomNav" variant="ghost" size="sm" :disabled="page <= 1" @click="$emit('prev')">
        <ChevronLeft :size="16" :stroke-width="1.75" />
        上一页
      </BaseButton>
      <BaseButton v-if="!bottomNav" variant="ghost" size="sm" :disabled="page >= totalPages" @click="$emit('next')">
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
