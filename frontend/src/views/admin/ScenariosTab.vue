<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { AdminScenario, ScenarioGenerationLog } from '@/api/adminTypesContent'
import { api } from '@/api/client'
import ScenarioGeneratePanel from '@/components/admin/scenarios/ScenarioGeneratePanel.vue'
import ScenarioGenerationLogModal from '@/components/admin/scenarios/ScenarioGenerationLogModal.vue'
import ScenarioListPanel from '@/components/admin/scenarios/ScenarioListPanel.vue'
import ScenarioTemplateCard from '@/components/admin/scenarios/ScenarioTemplateCard.vue'
import ScenarioUploadCard from '@/components/admin/scenarios/ScenarioUploadCard.vue'
import StoryCompilePanel from '@/components/admin/scenarios/StoryCompilePanel.vue'
import { errorMessage, usePagedList } from '@/components/admin/scenarios/pagedList'
import { useToastRun } from '@/components/admin/scenarios/useToastRun'
import ScenarioDetailModal from '@/components/scenario/ScenarioDetailModal.vue'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
const run = useToastRun()
const list = usePagedList<AdminScenario>('/api/admin/scenarios')

const viewing = ref<AdminScenario | null>(null)
const detailOpen = ref(false)

const logOpen = ref(false)
const logLoading = ref(false)
const genLog = ref<ScenarioGenerationLog | null>(null)

onMounted(() => run(() => list.load()))

// NOTE: 生成/导入/编译成功后旧前端一律回到第 1 页（新模组按创建时间倒序排在最前）
const reloadFirstPage = () => list.load(1)

function view(s: AdminScenario) {
  viewing.value = s
  detailOpen.value = true
}

async function viewLog(s: AdminScenario) {
  if (!s?.id) return
  genLog.value = {
    scenario_id: s.id,
    scenario_name: s.name || '',
    has_log: false,
    log_text: '',
    has_story: false,
    story_document: '',
  }
  logLoading.value = true
  logOpen.value = true
  try {
    const resp = await api<ScenarioGenerationLog>('GET', '/api/admin/scenarios/' + s.id + '/generation-log')
    genLog.value = resp || genLog.value
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
    logOpen.value = false
  }
  logLoading.value = false
}

async function remove(id: number) {
  if (!(await ui.confirm('确认删除该模组？此操作不可逆。', { danger: true, confirmText: '删除' }))) return
  try {
    await api('DELETE', '/api/scenarios/' + id)
    ui.toast('模组已删除')
    await list.reloadAfterDelete()
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  }
}

async function clearAll() {
  if (!(await ui.confirm('确认清空所有模组？此操作不可逆，将删除全部已有模组。', { danger: true, confirmText: '清空' }))) return
  try {
    await api('DELETE', '/api/scenarios')
    ui.toast('模组已清空')
    await list.load(1)
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  }
}
</script>

<template>
  <div class="space-y-5">
    <ScenarioListPanel
      :items="list.items.value"
      :page="list.page.value"
      :page-size="list.pageSize.value"
      :total="list.total.value"
      :total-pages="list.totalPages.value"
      @prev="run(list.prev)"
      @next="run(list.next)"
      @refresh="run(() => list.load(list.page.value))"
      @page-size="(n) => ((list.pageSize.value = n), run(list.changePageSize))"
      @clear="clearAll"
      @view="view"
      @log="viewLog"
      @delete="remove"
    />
    <ScenarioTemplateCard />
    <ScenarioUploadCard :reload="reloadFirstPage" />
    <ScenarioGeneratePanel :reload="reloadFirstPage" />
    <StoryCompilePanel :reload="reloadFirstPage" />

    <ScenarioDetailModal :open="detailOpen" :scenario="viewing" @close="detailOpen = false" />
    <ScenarioGenerationLogModal :open="logOpen" :loading="logLoading" :log="genLog" @close="logOpen = false" />
  </div>
</template>
