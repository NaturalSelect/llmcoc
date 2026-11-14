<script setup lang="ts">
import { Eye, Trash2 } from '@lucide/vue'
import type { OnlineModule } from '@/api/adminTypesOnline'
import PagerHeader from '@/components/admin/scenarios/PagerHeader.vue'
import { PAGE_SIZES } from '@/components/admin/scenarios/pagedList'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { formatTime, moduleMetaLine, statusLabel, statusTone } from './onlineModules'

defineProps<{ items: OnlineModule[]; page: number; pageSize: number; total: number; totalPages: number }>()
const emit = defineEmits<{
  prev: []
  next: []
  refresh: []
  pageSize: [size: number]
  view: [id: number]
  delete: [module: OnlineModule]
}>()

function byline(m: OnlineModule): string {
  return [m.author ? '作者 ' + m.author : '', moduleMetaLine(m.meta), formatTime(m.updated_at)].filter(Boolean).join(' · ')
}
</script>

<template>
  <PaperCard>
    <PagerHeader
      title="已下载的线上模组"
      unit="个"
      :page="page"
      :total-pages="totalPages"
      :total="total"
      @prev="emit('prev')"
      @next="emit('next')"
      @refresh="emit('refresh')"
    >
      <template #before>
        <label for="admin-online-page-size" class="text-xs text-fg-subtle">每页</label>
        <div class="w-20">
          <BaseSelect id="admin-online-page-size" :model-value="pageSize" @update:model-value="emit('pageSize', Number($event))">
            <option v-for="n in PAGE_SIZES" :key="n" :value="n">{{ n }}</option>
          </BaseSelect>
        </div>
      </template>
    </PagerHeader>

    <EmptyState v-if="items.length === 0">还没有下载过线上模组，到「浏览线上」里挑一个吧</EmptyState>
    <ul v-else class="space-y-2">
      <li v-for="m in items" :key="m.id" class="flex items-center gap-3 rounded-lg bg-surface-2 px-3 py-2">
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <p class="truncate text-sm font-medium text-fg">{{ m.title }}</p>
            <StampBadge :tone="statusTone(m.status, m.scenario_id)">{{ statusLabel(m.status, m.scenario_id) }}</StampBadge>
            <span v-if="m.scenario_id != null" class="text-xs tabular-nums text-fg-subtle">模组 #{{ m.scenario_id }}</span>
          </div>
          <p class="mt-0.5 truncate text-xs tabular-nums text-fg-subtle">{{ byline(m) }}</p>
          <p v-if="m.status === 'failed' && m.error" class="mt-0.5 line-clamp-1 text-xs break-words text-danger">{{ m.error }}</p>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <BaseButton variant="ghost" size="sm" @click="emit('view', m.id)">
            <Eye :size="16" :stroke-width="1.75" />
            详情
          </BaseButton>
          <BaseButton variant="danger" size="sm" @click="emit('delete', m)">
            <Trash2 :size="16" :stroke-width="1.75" />
            删除
          </BaseButton>
        </div>
      </li>
    </ul>
  </PaperCard>
</template>
