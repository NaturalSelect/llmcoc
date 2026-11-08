<script setup lang="ts">
import { RefreshCw, Trash2 } from '@lucide/vue'
import type { CacheStats } from '@/api/adminTypesCore'
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { cacheTotal, capacityText, capacityWidth, hitRateText, hitRateWidth } from './cacheFormat'

defineProps<{ stats: CacheStats | null }>()
defineEmits<{ refresh: []; clear: [] }>()
</script>

<template>
  <PaperCard tag="section" aria-labelledby="cache-stats-title" class="max-w-xl">
    <div class="mb-4 flex items-center justify-between gap-3">
      <SectionHeading id="cache-stats-title">规则缓存统计</SectionHeading>
      <div class="flex gap-2">
        <BaseButton variant="ghost" size="sm" @click="$emit('refresh')"><RefreshCw :size="16" :stroke-width="1.75" />刷新</BaseButton>
        <BaseButton variant="danger" size="sm" @click="$emit('clear')"><Trash2 :size="16" :stroke-width="1.75" />清空缓存</BaseButton>
      </div>
    </div>

    <p v-if="!stats" class="text-sm text-fg-subtle" role="status">加载中…</p>
    <div v-else class="space-y-4">
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div class="rounded-lg bg-surface-2 p-3">
          <div class="text-xs text-fg-subtle">完全命中</div>
          <div class="mt-1 text-lg tabular-nums text-fg">{{ stats.full_hits ?? 0 }}</div>
          <div class="mt-1 text-xs text-fg-subtle">LLM没搜索规则书</div>
        </div>
        <div class="rounded-lg bg-surface-2 p-3">
          <div class="text-xs text-fg-subtle">部分命中</div>
          <div class="mt-1 text-lg tabular-nums text-fg">{{ stats.partial_hits ?? 0 }}</div>
          <div class="mt-1 text-xs text-fg-subtle">缓存有结果但仍搜了规则书</div>
        </div>
        <div class="rounded-lg bg-surface-2 p-3">
          <div class="text-xs text-fg-subtle">未命中</div>
          <div class="mt-1 text-lg tabular-nums text-fg">{{ stats.misses ?? 0 }}</div>
          <div class="mt-1 text-xs text-fg-subtle">search_cache无结果，完全依赖规则书</div>
        </div>
      </div>

      <div v-if="cacheTotal(stats) > 0">
        <div class="mb-1.5 flex justify-between text-xs tabular-nums text-fg-muted">
          <span>命中率</span>
          <span>{{ hitRateText(stats) }}</span>
        </div>
        <div
          class="h-2 w-full overflow-hidden rounded-full bg-surface-3"
          role="progressbar"
          aria-label="命中率"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-valuenow="Math.round(hitRateWidth(stats))"
        >
          <div class="h-full rounded-full bg-success" :style="{ width: hitRateWidth(stats) + '%' }" />
        </div>
      </div>

      <div>
        <div class="mb-1.5 flex justify-between text-xs tabular-nums text-fg-muted">
          <span>条目数 / 容量使用</span>
          <span>{{ capacityText(stats) }}</span>
        </div>
        <div
          class="h-2 w-full overflow-hidden rounded-full bg-surface-3"
          role="progressbar"
          aria-label="容量使用"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-valuenow="Math.round(capacityWidth(stats))"
        >
          <div class="h-full rounded-full bg-info" :style="{ width: capacityWidth(stats) + '%' }" />
        </div>
      </div>
    </div>
  </PaperCard>
</template>
