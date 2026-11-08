<script setup lang="ts">
import type { AdminScenario } from '@/api/adminTypesContent'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import PaperCard from '@/components/base/PaperCard.vue'
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
  <PaperCard class="lg:col-span-2">
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
        <label for="admin-scenario-page-size" class="font-type text-xs text-ink-faint">每页</label>
        <BaseSelect
          id="admin-scenario-page-size"
          class="!w-20 !px-2 !py-1 !text-xs"
          :model-value="pageSize"
          @update:model-value="emit('pageSize', Number($event))"
        >
          <option v-for="n in PAGE_SIZES" :key="n" :value="n">{{ n }}</option>
        </BaseSelect>
      </template>
      <template #after>
        <BaseButton variant="danger" class="!px-2.5 !py-1 !text-xs" @click="emit('clear')">清空所有模组</BaseButton>
      </template>
    </PagerHeader>

    <p v-if="items.length === 0" class="text-xs text-ink-faint">暂无模组</p>
    <ul class="space-y-2">
      <li v-for="s in items" :key="s.id" class="flex items-center gap-3 rounded-sm border border-paper-edge bg-paper-deep/40 p-2">
        <div class="min-w-0 flex-1">
          <p class="truncate text-sm text-ink">{{ s.name }}</p>
          <p class="mt-0.5 font-type text-[11px] text-ink-faint">{{ scenarioMeta(s) }}</p>
        </div>
        <BaseButton variant="ghost" class="shrink-0 !px-2.5 !py-1 !text-xs" @click="emit('view', s)">查看</BaseButton>
        <BaseButton variant="ghost" class="shrink-0 !px-2.5 !py-1 !text-xs" @click="emit('log', s)">LLM记录</BaseButton>
        <BaseButton variant="danger" class="shrink-0 !px-2.5 !py-1 !text-xs" @click="emit('delete', s.id)">删除</BaseButton>
      </li>
    </ul>
  </PaperCard>
</template>
