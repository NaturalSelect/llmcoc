<script setup lang="ts">
import { Info, ListChecks } from '@lucide/vue'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import type { OnlineModule } from '@/api/adminTypesOnline'
import { api } from '@/api/client'
import OnlineBrowsePanel from '@/components/admin/online/OnlineBrowsePanel.vue'
import OnlineModuleDetailModal from '@/components/admin/online/OnlineModuleDetailModal.vue'
import OnlineModuleList from '@/components/admin/online/OnlineModuleList.vue'
import { RIGHTS_NOTICE } from '@/components/admin/online/onlineModules'
import { errorMessage, usePagedList } from '@/components/admin/scenarios/pagedList'
import { useToastRun } from '@/components/admin/scenarios/useToastRun'
import BaseButton from '@/components/base/BaseButton.vue'
import TabBar from '@/components/base/TabBar.vue'
import { useOnlineTasksStore } from '@/stores/onlineTasks'
import { useUiStore } from '@/stores/ui'

const BASE = '/api/admin/online-modules'
const SECTIONS = [
  { key: 'browse', label: '浏览线上' },
  { key: 'local', label: '已下载' },
]

const ui = useUiStore()
const router = useRouter()
const tasks = useOnlineTasksStore()
const run = useToastRun()
const list = usePagedList<OnlineModule>(BASE)

const section = ref<'browse' | 'local'>('browse')
const browse = ref<InstanceType<typeof OnlineBrowsePanel> | null>(null)
const detailId = ref<number | null>(null)
const detailOpen = ref(false)

// NOTE: 下载和上传都在后台跑，结果经 WebSocket 推送：每次变化同步浏览区的状态标签，任务结束时再刷新本地列表
const offChange = tasks.onChange(({ module, settled }) => {
  browse.value?.applyModule(module)
  if (settled) void run(() => list.load(list.page.value))
})

onMounted(() => {
  tasks.acquire()
  void run(() => list.load())
})
onBeforeUnmount(() => {
  offChange()
  tasks.release()
})

// 浏览区用 v-show 保留搜索词和页码；本地列表每次切回来都刷新，才能看到刚下载的条目
watch(section, (s) => {
  if (s === 'local') void run(() => list.load(list.page.value))
})

function openDetail(id: number) {
  detailId.value = id
  detailOpen.value = true
}

// 弹窗里的下载/放行/上传/编译都会改变本地条目：同步浏览区的状态标签并刷新本地列表
function onChanged(m: OnlineModule) {
  browse.value?.applyModule(m)
  void run(() => list.load(list.page.value))
}

async function remove(m: OnlineModule) {
  if (!(await ui.confirm(`确认删除「${m.title}」及其提取的文档？已编译入库的模组不受影响。`, { danger: true, confirmText: '删除' }))) return
  try {
    await api('DELETE', `${BASE}/${m.id}`)
    ui.toast('已删除')
    browse.value?.applyDeleted(m.id)
    if (detailId.value === m.id) detailOpen.value = false
    await list.reloadAfterDelete()
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 px-4 py-3" role="note">
      <Info :size="16" :stroke-width="1.75" class="mt-0.5 shrink-0 text-warning" />
      <p class="text-sm text-fg">{{ RIGHTS_NOTICE }}</p>
    </div>

    <div class="flex items-end gap-3">
      <TabBar v-model="section" :tabs="SECTIONS" class="min-w-0 flex-1" />
      <BaseButton variant="secondary" size="sm" @click="router.push({ name: 'admin-online-tasks' })">
        <ListChecks :size="16" :stroke-width="1.75" />
        任务进度
      </BaseButton>
    </div>
    <OnlineBrowsePanel v-show="section === 'browse'" ref="browse" @open="openDetail" />
    <OnlineModuleList
      v-if="section === 'local'"
      :items="list.items.value"
      :page="list.page.value"
      :page-size="list.pageSize.value"
      :total="list.total.value"
      :total-pages="list.totalPages.value"
      @prev="run(list.prev)"
      @next="run(list.next)"
      @refresh="run(() => list.load(list.page.value))"
      @page-size="(n) => ((list.pageSize.value = n), run(list.changePageSize))"
      @view="openDetail"
      @delete="remove"
    />

    <OnlineModuleDetailModal :open="detailOpen" :module-id="detailId" @close="detailOpen = false" @changed="onChanged" />
  </div>
</template>
