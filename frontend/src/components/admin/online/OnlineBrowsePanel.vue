<script setup lang="ts">
import { Download, ExternalLink, Eye, Search, TriangleAlert } from '@lucide/vue'
import { onMounted, reactive, ref } from 'vue'
import type { BrowseItem, BrowsePage, OnlineModule } from '@/api/adminTypesOnline'
import { api } from '@/api/client'
import PagerHeader from '@/components/admin/scenarios/PagerHeader.vue'
import { errorMessage } from '@/components/admin/scenarios/pagedList'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import Skeleton from '@/components/base/Skeleton.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { useUiStore } from '@/stores/ui'
import {
  DOWNLOAD_FAILED_HINT,
  cnmodsByline,
  cnmodsMetaLine,
  opinionText,
  safeUrl,
  statusLabel,
  statusTone,
  toCnmodsItem,
} from './onlineModules'

const BASE = '/api/admin/online-modules'

const emit = defineEmits<{ open: [id: number] }>()
const ui = useUiStore()

const keyword = ref('')
// 翻页和刷新沿用已提交的搜索词，输入框里还没提交的内容不生效
const appliedTitle = ref('')
const items = ref<BrowseItem[]>([])
const page = ref(1)
const totalPages = ref(1)
const total = ref(0)
const loading = ref(false)
const loadError = ref('')
// NOTE: 只防 POST 本身的重复点击（接口立即返回 202）；下载是否还在进行看 item.local.status
const downloading = reactive(new Set<string>())
// NOTE: 下载失败的条目：带上后端给出的失败原因；结果经 WebSocket 到达，由 applyModule 写入
const failures = reactive<Record<string, OnlineModule>>({})

// 快速翻页时较早发出的请求可能后返回，只采用最后一次的结果
let latest = 0

async function load(p: number = page.value): Promise<void> {
  const seq = ++latest
  loading.value = true
  loadError.value = ''
  const query = `?page=${encodeURIComponent(p)}` + (appliedTitle.value ? `&title=${encodeURIComponent(appliedTitle.value)}` : '')
  try {
    const resp = await api<BrowsePage>('GET', `${BASE}/browse${query}`)
    if (seq !== latest) return
    items.value = resp.items || []
    page.value = Math.max(1, Number(resp.page || p))
    totalPages.value = Math.max(1, Number(resp.total_pages || 1))
    total.value = Math.max(0, Number(resp.total_elements || 0))
  } catch (e) {
    if (seq !== latest) return
    loadError.value = errorMessage(e)
    ui.toast(loadError.value, 'error')
  } finally {
    if (seq === latest) loading.value = false
  }
}

function goto(p: number) {
  void load(Math.min(Math.max(1, p), totalPages.value))
}

function search() {
  appliedTitle.value = keyword.value.trim()
  void load(1)
}

// 本地条目的状态变化（下载、放行、上传、编译）同步到列表行，不重新请求来源站
function applyModule(m: OnlineModule): void {
  for (const it of items.value) {
    if (it.keyId !== m.source_key) continue
    it.local = { id: m.id, status: m.status, scenario_id: m.scenario_id }
    if (m.status === 'failed') failures[it.keyId] = m
    else delete failures[it.keyId]
  }
}

function applyDeleted(id: number): void {
  for (const it of items.value) {
    if (it.local?.id === id) {
      it.local = null
      delete failures[it.keyId]
    }
  }
}

async function download(item: BrowseItem) {
  if (busy(item)) return
  downloading.add(item.keyId)
  delete failures[item.keyId]
  try {
    const m = await api<OnlineModule>('POST', `${BASE}/download`, toCnmodsItem(item))
    applyModule(m)
    ui.toast(`已开始下载：${item.title}，可在「任务进度」页查看`)
  } catch (e) {
    // 409：该条目已有下载在进行，后端消息原样提示
    ui.toast(errorMessage(e), 'error')
  } finally {
    downloading.delete(item.keyId)
  }
}

function busy(item: BrowseItem): boolean {
  return downloading.has(item.keyId) || item.local?.status === 'downloading'
}

function downloadLabel(item: BrowseItem): string {
  if (busy(item)) return '下载中…'
  return item.local?.status === 'failed' ? '重新下载' : '下载'
}

function showFailure(item: BrowseItem): boolean {
  return !!failures[item.keyId] || item.local?.status === 'failed'
}

onMounted(() => void load(1))

defineExpose({ applyModule, applyDeleted, reload: () => load() })
</script>

<template>
  <PaperCard>
    <PagerHeader
      title="浏览线上模组"
      unit="个"
      :page="page"
      :total-pages="totalPages"
      :total="total"
      @prev="goto(page - 1)"
      @next="goto(page + 1)"
      @refresh="load()"
    >
      <template #before>
        <form class="flex items-center gap-2" @submit.prevent="search">
          <div class="w-56">
            <BaseInput v-model="keyword" type="search" placeholder="按标题搜索" aria-label="按标题搜索" />
          </div>
          <BaseButton type="submit" variant="secondary" size="sm" :disabled="loading">
            <Search :size="16" :stroke-width="1.75" />
            搜索
          </BaseButton>
        </form>
      </template>
    </PagerHeader>

    <div v-if="loading && items.length === 0" class="space-y-2" aria-busy="true">
      <Skeleton class="h-20" />
      <Skeleton class="h-20" />
      <Skeleton class="h-20" />
    </div>
    <EmptyState v-else-if="items.length === 0">
      {{ loadError ? '加载失败：' + loadError : '没有找到模组' }}
    </EmptyState>
    <ul v-else class="space-y-2 transition-opacity" :class="{ 'opacity-60': loading }">
      <li v-for="item in items" :key="item.keyId" class="rounded-lg bg-surface-2 px-3 py-3">
        <div class="flex items-start gap-3">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <p class="truncate text-sm font-medium text-fg">{{ item.title }}</p>
              <StampBadge :tone="statusTone(item.local?.status, item.local?.scenario_id)">
                {{ statusLabel(item.local?.status, item.local?.scenario_id) }}
              </StampBadge>
              <StampBadge v-if="item.moduleType">{{ item.moduleType }}</StampBadge>
            </div>
            <p v-if="cnmodsByline(item)" class="mt-0.5 text-xs text-fg-subtle">{{ cnmodsByline(item) }}</p>
            <p v-if="cnmodsMetaLine(item)" class="mt-0.5 text-xs tabular-nums text-fg-subtle">{{ cnmodsMetaLine(item) }}</p>
            <p v-if="opinionText(item.opinion)" class="mt-1 line-clamp-2 text-xs break-words text-fg-muted">{{ opinionText(item.opinion) }}</p>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <BaseButton v-if="item.local?.status !== 'extracted'" size="sm" :loading="busy(item)" @click="download(item)">
              <Download v-if="!busy(item)" :size="16" :stroke-width="1.75" />
              {{ downloadLabel(item) }}
            </BaseButton>
            <BaseButton v-if="item.local" variant="ghost" size="sm" @click="emit('open', item.local.id)">
              <Eye :size="16" :stroke-width="1.75" />
              详情
            </BaseButton>
          </div>
        </div>

        <div v-if="showFailure(item)" class="mt-3 rounded-lg border border-danger/40 bg-danger/10 p-3" role="alert">
          <p class="flex items-center gap-1.5 text-sm text-fg">
            <TriangleAlert :size="16" :stroke-width="1.75" class="shrink-0 text-danger" />
            {{ DOWNLOAD_FAILED_HINT }}
          </p>
          <p v-if="failures[item.keyId]?.error" class="mt-1 text-xs break-words text-fg-muted">{{ failures[item.keyId]?.error }}</p>
          <a
            v-if="safeUrl(item.url)"
            :href="safeUrl(item.url)"
            target="_blank"
            rel="noopener noreferrer"
            class="mt-2 inline-flex items-center gap-1 text-sm text-accent hover:text-accent-strong hover:underline"
          >
            <ExternalLink :size="14" :stroke-width="1.75" />
            打开分享页
          </a>
        </div>
      </li>
    </ul>
  </PaperCard>
</template>
