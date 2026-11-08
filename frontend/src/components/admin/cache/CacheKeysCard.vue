<script setup lang="ts">
import { ChevronLeft, ChevronRight, Eye, RefreshCw, Trash2 } from '@lucide/vue'
import BaseButton from '@/components/base/BaseButton.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'

defineProps<{ keys: string[]; total: number; page: number; totalPages: number }>()
defineEmits<{ refresh: []; prev: []; next: []; view: [key: string]; remove: [key: string] }>()
</script>

<template>
  <PaperCard tag="section" aria-labelledby="cache-keys-title" class="max-w-4xl">
    <div class="mb-4 flex items-center justify-between gap-3">
      <SectionHeading id="cache-keys-title">缓存条目列表</SectionHeading>
      <BaseButton variant="ghost" size="sm" @click="$emit('refresh')"><RefreshCw :size="16" :stroke-width="1.75" />刷新</BaseButton>
    </div>

    <EmptyState v-if="total === 0">暂无缓存条目</EmptyState>
    <div v-else>
      <ul class="divide-y divide-line">
        <li v-for="key in keys" :key="key" class="flex items-center justify-between gap-3 py-2">
          <span class="min-w-0 flex-1 truncate font-mono text-xs text-fg-muted" :title="key">{{ key }}</span>
          <div class="flex shrink-0 gap-2">
            <BaseButton variant="ghost" size="sm" @click="$emit('view', key)"><Eye :size="16" :stroke-width="1.75" />查看</BaseButton>
            <BaseButton variant="danger" size="sm" @click="$emit('remove', key)"><Trash2 :size="16" :stroke-width="1.75" />删除</BaseButton>
          </div>
        </li>
      </ul>

      <div class="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-line pt-3">
        <span class="text-xs tabular-nums text-fg-subtle">{{ total }} 条 · 第 {{ page }}/{{ totalPages }} 页</span>
        <div class="flex gap-2">
          <BaseButton variant="ghost" size="sm" :disabled="page <= 1" @click="$emit('prev')">
            <ChevronLeft :size="16" :stroke-width="1.75" />上一页
          </BaseButton>
          <BaseButton variant="ghost" size="sm" :disabled="page >= totalPages" @click="$emit('next')">
            下一页<ChevronRight :size="16" :stroke-width="1.75" />
          </BaseButton>
        </div>
      </div>
    </div>
  </PaperCard>
</template>
