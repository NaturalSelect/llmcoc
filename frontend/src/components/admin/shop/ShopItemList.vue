<script setup lang="ts">
import type { AdminShopItem } from '@/api/adminTypesContent'
import BaseButton from '@/components/base/BaseButton.vue'
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

    <p v-if="items.length === 0" class="py-8 text-center text-xs text-ink-faint">暂无已上架商品</p>

    <ul v-else class="max-h-[30rem] space-y-2 overflow-y-auto pr-1">
      <li
        v-for="item in items"
        :key="item.id"
        class="flex items-start justify-between gap-3 rounded-sm border border-paper-edge bg-paper-deep/40 p-3"
      >
        <div class="min-w-0 flex-1">
          <div class="mb-1 flex flex-wrap items-center gap-2">
            <span class="text-sm font-bold text-ink">{{ item.name }}</span>
            <StampBadge>{{ item.item_type }}</StampBadge>
            <span class="font-type text-[10px] text-ink-faint">ID {{ item.id }}</span>
          </div>
          <p v-if="item.description" class="text-xs break-words text-ink-soft">{{ item.description }}</p>
          <div class="mt-2 flex flex-wrap gap-3 font-type text-xs">
            <span class="text-seal">价格: {{ item.price }} 金币</span>
            <span class="text-ink-faint">效果值: {{ item.value }}</span>
          </div>
        </div>
        <BaseButton variant="danger" class="shrink-0 !px-2.5 !py-1 !text-xs" @click="emit('delete', item)">删除</BaseButton>
      </li>
    </ul>
  </PaperCard>
</template>
