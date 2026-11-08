<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import type { AdminShopItem } from '@/api/adminTypesContent'
import BaseButton from '@/components/base/BaseButton.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import PagerHeader from '@/components/admin/scenarios/PagerHeader.vue'

defineProps<{ items: AdminShopItem[]; page: number; total: number; totalPages: number }>()
const emit = defineEmits<{ prev: []; next: []; refresh: []; delete: [item: AdminShopItem] }>()
</script>

<template>
  <PaperCard class="min-w-0">
    <PagerHeader
      title="已上架商品"
      unit="个"
      note="删除后会从商城下架，历史购买记录会保留。"
      :page="page"
      :total-pages="totalPages"
      :total="total"
      @prev="emit('prev')"
      @next="emit('next')"
      @refresh="emit('refresh')"
    />

    <EmptyState v-if="items.length === 0">暂无已上架商品</EmptyState>

    <ul v-else class="max-h-120 space-y-2 overflow-y-auto">
      <li v-for="item in items" :key="item.id" class="flex items-start justify-between gap-3 rounded-lg bg-surface-2 px-3 py-2">
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-sm font-medium text-fg">{{ item.name }}</span>
            <StampBadge>{{ item.item_type }}</StampBadge>
            <span class="font-mono text-xs text-fg-subtle">ID {{ item.id }}</span>
          </div>
          <p v-if="item.description" class="mt-1 text-sm break-words text-fg-muted">{{ item.description }}</p>
          <div class="mt-1.5 flex flex-wrap gap-3 text-xs tabular-nums">
            <span class="text-accent">价格: {{ item.price }} 金币</span>
            <span class="text-fg-subtle">效果值: {{ item.value }}</span>
          </div>
        </div>
        <BaseButton variant="danger" size="sm" class="shrink-0" @click="emit('delete', item)">
          <Trash2 :size="16" :stroke-width="1.75" />
          删除
        </BaseButton>
      </li>
    </ul>
  </PaperCard>
</template>
