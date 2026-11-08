<script setup lang="ts">
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'

defineProps<{ keys: string[]; total: number; page: number; totalPages: number }>()
defineEmits<{ refresh: []; prev: []; next: []; view: [key: string]; remove: [key: string] }>()
</script>

<template>
  <PaperCard tag="section" aria-labelledby="cache-keys-title" class="max-w-4xl">
    <div class="mb-4 flex items-center justify-between">
      <h3 id="cache-keys-title" class="font-type text-sm font-bold tracking-widest text-seal">缓存条目列表</h3>
      <BaseButton variant="ghost" class="!px-3 !py-1 !text-xs" @click="$emit('refresh')">刷新</BaseButton>
    </div>

    <p v-if="total === 0" class="text-sm text-ink-faint">暂无缓存条目</p>
    <div v-else class="space-y-1">
      <div
        v-for="key in keys"
        :key="key"
        class="flex items-center justify-between rounded-sm border border-paper-edge bg-paper-deep/50 px-3 py-1.5"
      >
        <span class="mr-2 min-w-0 flex-1 truncate font-type text-xs text-ink-soft" :title="key">{{ key }}</span>
        <div class="flex shrink-0 gap-1">
          <BaseButton variant="ghost" class="!px-2 !py-0.5 !text-xs" @click="$emit('view', key)">查看</BaseButton>
          <BaseButton variant="danger" class="!px-2 !py-0.5 !text-xs" @click="$emit('remove', key)">删除</BaseButton>
        </div>
      </div>

      <div class="mt-3 flex items-center justify-between border-t border-paper-edge pt-3">
        <span class="font-type text-xs text-ink-faint">{{ total }} 条 · 第 {{ page }}/{{ totalPages }} 页</span>
        <div class="flex gap-1">
          <BaseButton variant="ghost" class="!px-3 !py-1 !text-xs" :disabled="page <= 1" @click="$emit('prev')">上一页</BaseButton>
          <BaseButton variant="ghost" class="!px-3 !py-1 !text-xs" :disabled="page >= totalPages" @click="$emit('next')">下一页</BaseButton>
        </div>
      </div>
    </div>
  </PaperCard>
</template>
