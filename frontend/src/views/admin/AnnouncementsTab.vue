<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { Announcement } from '@/api/adminTypesContent'
import { api } from '@/api/client'
import { announcementStatusLabel, levelLabel } from '@/components/admin/announcements/announcementLogic'
import AnnouncementFormModal from '@/components/admin/announcements/AnnouncementFormModal.vue'
import PagerHeader from '@/components/admin/scenarios/PagerHeader.vue'
import { errorMessage, usePagedList } from '@/components/admin/scenarios/pagedList'
import { useToastRun } from '@/components/admin/scenarios/useToastRun'
import BaseButton from '@/components/base/BaseButton.vue'
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

// NOTE: 新主题里没有独立的警告色，用朱砂加粗区分警告与重要
function levelClass(level: string): string {
  if (level === 'important') return 'text-seal'
  if (level === 'warning') return 'font-bold text-seal'
  return 'text-ink-faint'
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
          <BaseButton class="!px-2.5 !py-1 !text-xs" @click="openCreate">+ 新建公告</BaseButton>
        </template>
      </PagerHeader>

      <p v-if="list.items.value.length === 0" class="text-xs text-ink-faint">暂无公告</p>
      <ul class="space-y-2">
        <li
          v-for="a in list.items.value"
          :key="a.id"
          class="flex items-center justify-between gap-3 border-b border-paper-edge pb-2 text-xs"
        >
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="truncate font-semibold text-ink">{{ a.title }}</span>
              <span :class="levelClass(a.level)">{{ levelLabel(a.level) }}</span>
              <StampBadge v-if="a.is_pinned" tone="seal">置顶</StampBadge>
              <span class="text-ink-faint">{{ announcementStatusLabel(a) }}</span>
            </div>
            <p class="mt-0.5 text-ink-faint">{{ new Date(a.created_at).toLocaleString() }}</p>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <button type="button" class="text-ledger transition hover:underline" @click="openEdit(a)">编辑</button>
            <button type="button" class="text-seal transition hover:underline" @click="remove(a.id)">删除</button>
          </div>
        </li>
      </ul>
    </PaperCard>

    <AnnouncementFormModal :open="modalOpen" :editing="editing" @close="modalOpen = false" @saved="onSaved" />
  </div>
</template>
