<script setup lang="ts">
import type { CacheEntry } from '@/api/adminTypesCore'
import PaperCard from '@/components/base/PaperCard.vue'

// NOTE: 加载中时 entry 只有 key（value 为空），size 未知；加载完成后才渲染 KEY / VALUE 两块
defineProps<{ loading: boolean; entry: CacheEntry | null }>()
defineEmits<{ close: [] }>()
</script>

<template>
  <PaperCard tag="section" aria-labelledby="cache-entry-title" class="max-w-4xl">
    <div class="mb-3 flex items-start justify-between gap-4">
      <div>
        <h3 id="cache-entry-title" class="font-type text-sm font-bold tracking-widest text-seal">缓存条目详情</h3>
        <p v-if="entry?.size !== undefined" class="mt-1 font-type text-xs text-ink-faint">大小：{{ entry?.size ?? 0 }} 字节</p>
      </div>
      <button type="button" class="shrink-0 text-xl leading-none text-ink-faint transition hover:text-seal" aria-label="关闭详情" @click="$emit('close')">
        ✕
      </button>
    </div>

    <p v-if="loading" class="text-sm text-ink-faint" role="status">详情加载中…</p>
    <div v-else-if="entry" class="space-y-3">
      <div>
        <div class="mb-1 font-type text-[10px] font-bold uppercase tracking-wider text-ink-soft">KEY</div>
        <pre class="overflow-x-auto whitespace-pre-wrap rounded-sm border border-paper-edge bg-paper-deep/60 p-2 font-type text-xs leading-normal text-ink">{{ entry.key || '' }}</pre>
      </div>
      <div>
        <div class="mb-1 font-type text-[10px] font-bold uppercase tracking-wider text-ink-soft">VALUE / 规则裁定文本</div>
        <pre class="max-h-[60vh] overflow-auto whitespace-pre-wrap rounded-sm border border-paper-edge bg-paper-deep/60 p-3 font-type text-xs leading-relaxed text-ink">{{ entry.value || '' }}</pre>
      </div>
    </div>
  </PaperCard>
</template>
