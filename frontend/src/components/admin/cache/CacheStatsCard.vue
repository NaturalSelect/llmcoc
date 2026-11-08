<script setup lang="ts">
import type { CacheStats } from '@/api/adminTypesCore'
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import { cacheTotal, capacityText, capacityWidth, hitRateText, hitRateWidth } from './cacheFormat'

defineProps<{ stats: CacheStats | null }>()
defineEmits<{ refresh: []; clear: [] }>()
</script>

<template>
  <PaperCard tag="section" aria-labelledby="cache-stats-title" class="max-w-xl">
    <div class="mb-4 flex items-center justify-between">
      <h3 id="cache-stats-title" class="font-type text-sm font-bold tracking-widest text-seal">规则缓存统计</h3>
      <div class="flex gap-2">
        <BaseButton variant="ghost" class="!px-3 !py-1 !text-xs" @click="$emit('refresh')">刷新</BaseButton>
        <BaseButton variant="danger" class="!px-3 !py-1 !text-xs" @click="$emit('clear')">清空缓存</BaseButton>
      </div>
    </div>

    <p v-if="!stats" class="text-sm text-ink-faint" role="status">加载中…</p>
    <div v-else class="space-y-3">
      <div class="grid grid-cols-3 gap-3">
        <div class="rounded-sm border border-moss/40 bg-moss/5 p-3 text-center">
          <div class="font-type text-2xl font-bold text-moss">{{ stats.full_hits ?? 0 }}</div>
          <div class="mt-1 text-xs text-ink">完全命中</div>
          <div class="mt-0.5 text-xs text-ink-faint">LLM没搜索规则书</div>
        </div>
        <div class="rounded-sm border border-ledger/40 bg-ledger/5 p-3 text-center">
          <div class="font-type text-2xl font-bold text-ledger">{{ stats.partial_hits ?? 0 }}</div>
          <div class="mt-1 text-xs text-ink">部分命中</div>
          <div class="mt-0.5 text-xs text-ink-faint">缓存有结果但仍搜了规则书</div>
        </div>
        <div class="rounded-sm border border-seal/40 bg-seal/5 p-3 text-center">
          <div class="font-type text-2xl font-bold text-seal">{{ stats.misses ?? 0 }}</div>
          <div class="mt-1 text-xs text-ink">未命中</div>
          <div class="mt-0.5 text-xs text-ink-faint">search_cache无结果，完全依赖规则书</div>
        </div>
      </div>

      <div v-if="cacheTotal(stats) > 0">
        <div class="mb-1 flex justify-between font-type text-xs text-ink-soft">
          <span>命中率</span>
          <span>{{ hitRateText(stats) }}</span>
        </div>
        <div
          class="h-2 w-full overflow-hidden rounded-full bg-paper-edge"
          role="progressbar"
          aria-label="命中率"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-valuenow="Math.round(hitRateWidth(stats))"
        >
          <div class="h-full rounded-full bg-moss" :style="{ width: hitRateWidth(stats) + '%' }" />
        </div>
      </div>

      <div class="mt-2 border-t border-paper-edge pt-3">
        <div class="mb-1 flex justify-between font-type text-xs text-ink-soft">
          <span>条目数 / 容量使用</span>
          <span>{{ capacityText(stats) }}</span>
        </div>
        <div
          class="h-1.5 w-full overflow-hidden rounded-full bg-paper-edge"
          role="progressbar"
          aria-label="容量使用"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-valuenow="Math.round(capacityWidth(stats))"
        >
          <div class="h-full rounded-full bg-ink-faint" :style="{ width: capacityWidth(stats) + '%' }" />
        </div>
      </div>
    </div>
  </PaperCard>
</template>
