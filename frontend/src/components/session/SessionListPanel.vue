<script setup lang="ts" generic="T extends { id: number }">
import { computed } from 'vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaginationBar from '@/components/base/PaginationBar.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import Skeleton from '@/components/base/Skeleton.vue'

// NOTE: 活跃房间/历史/收藏三个列表的外壳：依次是首屏骨架、列表、空状态、翻页，各自的卡片和按钮由 item 插槽提供
const props = defineProps<{
  items: T[]
  loading: boolean
  page: number
  totalPages: number
  total: number
  emptyText: string
}>()
const emit = defineEmits<{ prev: []; next: [] }>()
defineSlots<{ item(props: { item: T }): unknown }>()

// NOTE: PaginationBar 通过 v-model:page 给出目标页码，这里翻译回对外不变的 prev / next 事件，页码本身仍由上层的分页列表维护
const pageModel = computed({
  get: () => props.page,
  set: (target: number) => {
    if (target > props.page) emit('next')
    else if (target < props.page) emit('prev')
  },
})
</script>

<template>
  <div class="space-y-3" :aria-busy="loading">
    <div v-if="loading && items.length === 0" class="grid gap-3 md:grid-cols-2" aria-hidden="true">
      <PaperCard v-for="i in 4" :key="i">
        <Skeleton class="mb-3 h-5 w-1/3" />
        <Skeleton class="mb-2 h-4 w-1/2" />
        <Skeleton class="h-4 w-2/5" />
      </PaperCard>
    </div>

    <ul v-if="items.length > 0" class="grid gap-3 md:grid-cols-2">
      <li v-for="it in items" :key="it.id">
        <slot name="item" :item="it" />
      </li>
    </ul>

    <EmptyState v-else-if="!loading">{{ emptyText }}</EmptyState>

    <PaginationBar v-if="items.length > 0" v-model:page="pageModel" :total-pages="totalPages" :total="total" />
  </div>
</template>
