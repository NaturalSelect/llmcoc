<script setup lang="ts" generic="T extends { id: number }">
import PaperCard from '@/components/base/PaperCard.vue'
import Skeleton from '@/components/base/Skeleton.vue'
import SessionPager from './SessionPager.vue'

// NOTE: 活跃房间/历史/收藏三个列表的外壳：首屏骨架 → 列表 → 空状态 → 翻页，各自的卡片和按钮由 item 插槽提供
defineProps<{
  items: T[]
  loading: boolean
  page: number
  totalPages: number
  total: number
  emptyIcon: string
  emptyText: string
}>()
defineEmits<{ prev: []; next: [] }>()
defineSlots<{ item(props: { item: T }): unknown }>()
</script>

<template>
  <div class="space-y-3" :aria-busy="loading">
    <div v-if="loading && items.length === 0" class="space-y-3" aria-hidden="true">
      <PaperCard v-for="i in 3" :key="i">
        <Skeleton class="mb-2.5 h-4 w-1/3" />
        <Skeleton class="mb-1.5 h-3 w-1/2" />
        <Skeleton class="h-3 w-2/5" />
      </PaperCard>
    </div>

    <ul v-if="items.length > 0" class="space-y-3">
      <li v-for="it in items" :key="it.id">
        <slot name="item" :item="it" />
      </li>
    </ul>

    <div v-else-if="!loading" class="py-12 text-center text-ink-faint">
      <p class="mb-3 text-4xl" aria-hidden="true">{{ emptyIcon }}</p>
      <p class="font-serif text-sm">{{ emptyText }}</p>
    </div>

    <SessionPager v-if="items.length > 0" :page="page" :total-pages="totalPages" :total="total" @prev="$emit('prev')" @next="$emit('next')" />
  </div>
</template>
