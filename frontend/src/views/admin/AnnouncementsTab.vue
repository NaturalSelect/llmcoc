<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Pencil, Trash2 } from '@lucide/vue'
import type { Announcement } from '@/api/adminTypesContent'
import { api } from '@/api/client'
import { announcementStatusLabel, levelLabel } from '@/components/admin/announcements/announcementLogic'
import AnnouncementFormModal from '@/components/admin/announcements/AnnouncementFormModal.vue'
import PagerHeader from '@/components/admin/scenarios/PagerHeader.vue'
import { errorMessage, usePagedList } from '@/components/admin/scenarios/pagedList'
import { useToastRun } from '@/components/admin/scenarios/useToastRun'
import BaseButton from '@/components/base/BaseButton.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
const run = useToastRun()
const list = usePagedList<Announcement>('/api/admin/announcements')

const modalOpen = ref(false)
const editing = ref<Announcement | null>(null)

onMounted(() => run(() => list.load()))

function openCreate() {
  editing.value = null
  modalOpen.value = true
}

function openEdit(a: Announcement) {
  editing.value = a
  modalOpen.value = true
}

// NOTE: 保存成功先关弹窗再刷新当前页，刷新失败只弹 toast，不会把弹窗留在屏幕上
async function onSaved() {
  modalOpen.value = false
  await run(() => list.load(list.page.value))
}

async function remove(id: number) {
  if (!(await ui.confirm('确认删除此公告？', { danger: true, confirmText: '删除' }))) return
  try {
    await api('DELETE', '/api/admin/announcements/' + id)
    ui.toast('已删除')
    await list.reloadAfterDelete()
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  }
}

// NOTE: 等级用徽章色区分：警告用 danger，重要用 accent，普通用中性色
function levelTone(level: string): 'danger' | 'seal' | 'faint' {
  if (level === 'warning') return 'danger'
  if (level === 'important') return 'seal'
  return 'faint'
}
</script>

<template>
  <div class="space-y-5">
    <PaperCard>
      <PagerHeader
        title="公告管理"
        unit="条"
        :page="list.page.value"
        :total-pages="list.totalPages.value"
        :total="list.total.value"
        @prev="run(list.prev)"
        @next="run(list.next)"
        @refresh="run(() => list.load(list.page.value))"
      >
        <template #before>
          <!-- NOTE: 按钮文字里的 + 是现有测试按文本查找的一部分，不能换成图标 -->
          <BaseButton size="sm" @click="openCreate">+ 新建公告</BaseButton>
        </template>
      </PagerHeader>

      <EmptyState v-if="list.items.value.length === 0">暂无公告</EmptyState>
      <ul v-else class="space-y-2">
        <li v-for="a in list.items.value" :key="a.id" class="flex items-center justify-between gap-3 rounded-lg bg-surface-2 px-3 py-2">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="truncate text-sm font-medium text-fg">{{ a.title }}</span>
              <StampBadge :tone="levelTone(a.level)">{{ levelLabel(a.level) }}</StampBadge>
              <StampBadge v-if="a.is_pinned" tone="seal">置顶</StampBadge>
              <span class="text-xs text-fg-subtle">{{ announcementStatusLabel(a) }}</span>
            </div>
            <p class="mt-0.5 text-xs tabular-nums text-fg-subtle">{{ new Date(a.created_at).toLocaleString() }}</p>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <BaseButton variant="ghost" size="sm" @click="openEdit(a)">
              <Pencil :size="16" :stroke-width="1.75" />
              编辑
            </BaseButton>
            <BaseButton variant="danger" size="sm" @click="remove(a.id)">
              <Trash2 :size="16" :stroke-width="1.75" />
              删除
            </BaseButton>
          </div>
        </li>
      </ul>
    </PaperCard>

    <AnnouncementFormModal :open="modalOpen" :editing="editing" @close="modalOpen = false" @saved="onSaved" />
  </div>
</template>
