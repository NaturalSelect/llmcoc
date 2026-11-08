<script setup lang="ts">
import { Eye, ScrollText, Trash2 } from '@lucide/vue'
import type { AdminScenario } from '@/api/adminTypesContent'
import { splitTags } from '@/components/scenario/scenarioFormat'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { PAGE_SIZES } from './pagedList'
import PagerHeader from './PagerHeader.vue'
import { scenarioMeta } from './scenarioMeta'

defineProps<{ items: AdminScenario[]; page: number; pageSize: number; total: number; totalPages: number }>()
const emit = defineEmits<{
  prev: []
  next: []
  refresh: []
  clear: []
  pageSize: [size: number]
  view: [scenario: AdminScenario]
  log: [scenario: AdminScenario]
  delete: [id: number]
}>()
</script>

<template>
  <PaperCard>
    <PagerHeader
      title="已有模组"
      unit="个"
      :page="page"
      :total-pages="totalPages"
      :total="total"
      @prev="emit('prev')"
      @next="emit('next')"
      @refresh="emit('refresh')"
    >
      <template #before>
        <label for="admin-scenario-page-size" class="text-xs text-fg-subtle">每页</label>
        <div class="w-20">
          <BaseSelect
            id="admin-scenario-page-size"
            :model-value="pageSize"
            @update:model-value="emit('pageSize', Number($event))"
          >
            <option v-for="n in PAGE_SIZES" :key="n" :value="n">{{ n }}</option>
          </BaseSelect>
        </div>
      </template>
      <template #after>
        <BaseButton variant="danger" size="sm" @click="emit('clear')">清空所有模组</BaseButton>
      </template>
    </PagerHeader>

    <EmptyState v-if="items.length === 0">暂无模组</EmptyState>
    <ul v-else class="space-y-2">
      <li v-for="s in items" :key="s.id" class="flex items-center gap-3 rounded-lg bg-surface-2 px-3 py-2">
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <p class="truncate text-sm text-fg">{{ s.name }}</p>
            <StampBadge v-for="tag in splitTags(s.tags)" :key="tag">{{ tag }}</StampBadge>
          </div>
          <p class="mt-0.5 text-xs tabular-nums text-fg-subtle">{{ scenarioMeta(s) }}</p>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <BaseButton variant="ghost" size="sm" @click="emit('view', s)">
            <Eye :size="16" :stroke-width="1.75" />
            查看
          </BaseButton>
          <BaseButton variant="ghost" size="sm" @click="emit('log', s)">
            <ScrollText :size="16" :stroke-width="1.75" />
            LLM记录
          </BaseButton>
          <BaseButton variant="danger" size="sm" @click="emit('delete', s.id)">
            <Trash2 :size="16" :stroke-width="1.75" />
            删除
          </BaseButton>
        </div>
      </li>
    </ul>
  </PaperCard>
</template>
