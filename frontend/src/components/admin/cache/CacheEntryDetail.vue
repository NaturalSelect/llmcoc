<script setup lang="ts">
import { X } from '@lucide/vue'
import type { CacheEntry } from '@/api/adminTypesCore'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'

// NOTE: 加载中时 entry 只有 key（value 为空），size 未知；加载完成后才渲染 KEY / VALUE 两块
defineProps<{ loading: boolean; entry: CacheEntry | null }>()
defineEmits<{ close: [] }>()
</script>

<template>
  <PaperCard tag="section" aria-labelledby="cache-entry-title" class="max-w-4xl">
    <div class="mb-4 flex items-start justify-between gap-4">
      <div>
        <SectionHeading id="cache-entry-title">缓存条目详情</SectionHeading>
        <p v-if="entry?.size !== undefined" class="mt-1 text-xs tabular-nums text-fg-subtle">大小：{{ entry?.size ?? 0 }} 字节</p>
      </div>
      <button
        type="button"
        class="shrink-0 rounded-lg p-1.5 text-fg-subtle transition hover:bg-surface-3 hover:text-fg"
        aria-label="关闭详情"
        @click="$emit('close')"
      >
        <X :size="16" :stroke-width="1.75" />
      </button>
    </div>

    <p v-if="loading" class="text-sm text-fg-subtle" role="status">详情加载中…</p>
    <div v-else-if="entry" class="space-y-4">
      <div>
        <div class="mb-1.5 text-xs text-fg-subtle">KEY</div>
        <pre class="overflow-x-auto rounded-lg bg-surface-2 p-3 font-mono text-xs leading-normal whitespace-pre-wrap text-fg">{{ entry.key || '' }}</pre>
      </div>
      <div>
        <div class="mb-1.5 text-xs text-fg-subtle">VALUE / 规则裁定文本</div>
        <pre class="max-h-[60vh] overflow-auto rounded-lg bg-surface-2 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-fg">{{ entry.value || '' }}</pre>
      </div>
    </div>
  </PaperCard>
</template>
