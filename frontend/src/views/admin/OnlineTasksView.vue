<script setup lang="ts">
import { ArrowLeft, Eye, Wifi, WifiOff } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import OnlineModuleDetailModal from '@/components/admin/online/OnlineModuleDetailModal.vue'
import OnlineTaskProgress from '@/components/admin/online/OnlineTaskProgress.vue'
import { sortTaskModules, statusLabel, statusTone } from '@/components/admin/online/onlineModules'
import { useNow } from '@/components/admin/online/useNow'
import BaseButton from '@/components/base/BaseButton.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PageHeader from '@/components/base/PageHeader.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import Skeleton from '@/components/base/Skeleton.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { useOnlineTasksStore } from '@/stores/onlineTasks'

const router = useRouter()
const tasks = useOnlineTasksStore()

const rows = computed(() => sortTaskModules(Object.values(tasks.modules)))
const hasActive = computed(() => rows.value.some((m) => m.status === 'downloading'))
const now = useNow(hasActive)

const detailId = ref<number | null>(null)
const detailOpen = ref(false)

onMounted(() => tasks.acquire())
onBeforeUnmount(() => tasks.release())

function openDetail(id: number) {
  detailId.value = id
  detailOpen.value = true
}

function back() {
  void router.push({ name: 'admin', params: { tab: 'online' } })
}
</script>

<template>
  <div class="mx-auto w-full max-w-6xl space-y-5 p-5">
    <PageHeader title="线上模组任务进度" subtitle="下载、上传后的提取正文和 OCR 都在后台运行，进度实时推送到这里。">
      <template #actions>
        <span
          class="inline-flex items-center gap-1.5 text-sm"
          :class="tasks.connected ? 'text-success' : 'text-warning'"
          role="status"
          data-testid="connection-indicator"
        >
          <Wifi v-if="tasks.connected" :size="16" :stroke-width="1.75" />
          <WifiOff v-else :size="16" :stroke-width="1.75" />
          {{ tasks.connected ? '已连接' : '重新连接中…' }}
        </span>
        <BaseButton variant="secondary" size="sm" @click="back">
          <ArrowLeft :size="16" :stroke-width="1.75" />
          返回线上模组
        </BaseButton>
      </template>
    </PageHeader>

    <PaperCard>
      <div v-if="!tasks.loaded && rows.length === 0" class="space-y-2" aria-busy="true">
        <Skeleton class="h-16" />
        <Skeleton class="h-16" />
      </div>
      <EmptyState v-else-if="rows.length === 0">还没有任务记录，到「浏览线上」里下载一个模组吧</EmptyState>
      <ul v-else class="space-y-2" aria-label="任务列表">
        <li v-for="m in rows" :key="m.id" class="flex items-start gap-3 rounded-lg bg-surface-2 px-3 py-3" data-testid="task-row">
          <div class="min-w-0 flex-1 space-y-1">
            <div class="flex flex-wrap items-center gap-2">
              <p class="truncate text-sm font-medium text-fg">{{ m.title }}</p>
              <StampBadge :tone="statusTone(m.status, m.scenario_id)">{{ statusLabel(m.status, m.scenario_id) }}</StampBadge>
            </div>
            <p v-if="m.author" class="text-xs text-fg-subtle">作者 {{ m.author }}</p>
            <OnlineTaskProgress v-if="m.status === 'downloading'" :module="m" :now="now" />
            <p v-if="m.error" class="text-xs break-words text-danger" data-testid="task-error">{{ m.error }}</p>
          </div>
          <BaseButton variant="ghost" size="sm" class="shrink-0" @click="openDetail(m.id)">
            <Eye :size="16" :stroke-width="1.75" />
            查看
          </BaseButton>
        </li>
      </ul>
    </PaperCard>

    <OnlineModuleDetailModal :open="detailOpen" :module-id="detailId" @close="detailOpen = false" />
  </div>
</template>
