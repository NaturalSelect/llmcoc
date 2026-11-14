<script setup lang="ts">
import { ExternalLink, FileText, Hammer, RefreshCw, ShieldAlert, ShieldCheck, TriangleAlert, Upload } from '@lucide/vue'
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import type { OnlineModule, OnlineModuleDetail, OnlineModuleDocumentFull } from '@/api/adminTypesOnline'
import { api, postForm } from '@/api/client'
import { streamSSE } from '@/api/sse'
import { errorMessage } from '@/components/admin/scenarios/pagedList'
import { createCompileHandler, formatLogLine, summarizeCompile } from '@/components/admin/scenarios/scenarioStream'
import StreamLogBox from '@/components/admin/scenarios/StreamLogBox.vue'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import FormField from '@/components/base/FormField.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import Skeleton from '@/components/base/Skeleton.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { useUiStore } from '@/stores/ui'
import { useOnlineTasksStore } from '@/stores/onlineTasks'
import OnlineTaskProgress from './OnlineTaskProgress.vue'
import { useNow } from './useNow'
import {
  DOWNLOAD_FAILED_HINT,
  PREVIEW_RUNES,
  bytesAmbiguous,
  clipForDisplay,
  compileBlockReason,
  defaultDocument,
  formatBytes,
  formatTime,
  moduleMetaLine,
  opinionText,
  safeUrl,
  splitByHits,
  statusLabel,
  statusTone,
  utf8Bytes,
  validateArchiveFile,
} from './onlineModules'

const BASE = '/api/admin/online-modules'

const props = defineProps<{ open: boolean; moduleId: number | null }>()
const emit = defineEmits<{ close: []; changed: [module: OnlineModule] }>()
const ui = useUiStore()
const tasks = useOnlineTasksStore()

const detail = ref<OnlineModuleDetail | null>(null)
const loading = ref(false)
const loadError = ref('')
const selectedId = ref<number | null>(null)

// 全文缓存：编译体积判断和「查看全文」共用，换文档或重新上传后按文档 id 清理
const texts = reactive<Record<number, string>>({})
const byteCounts = reactive<Record<number, number>>({})
const loadingDocId = ref<number | null>(null)
const textError = ref('')
const showFull = ref(false)

const overriding = ref(false)
const uploading = ref(false)
const compiling = ref(false)
const compileName = ref('')
const logs = ref<string[]>([])
const fileInput = ref<HTMLInputElement | null>(null)

let controller: AbortController | null = null
// 切换条目或关闭弹窗后，较早发出的详情请求不能再覆盖当前内容
let loadSeq = 0

const mod = computed(() => detail.value?.module ?? null)
const docs = computed(() => detail.value?.documents ?? [])
const selected = computed(() => docs.value.find((d) => d.id === selectedId.value) ?? null)
const loadedBytes = computed(() => (selected.value ? byteCounts[selected.value.id] : undefined))
const textLoading = computed(() => selected.value != null && loadingDocId.value === selected.value.id)
const shareUrl = computed(() => safeUrl(mod.value?.share_url))
const opinion = computed(() => opinionText(mod.value?.meta?.opinion))
const metaLine = computed(() => moduleMetaLine(mod.value?.meta))
const hits = computed(() => selected.value?.license_hits ?? [])
const overridden = computed(() => mod.value?.license_override_at != null)
// NOTE: 后台任务（下载或上传后的提取）进行中：此时不能再上传，耗时靠 now 每秒刷新（只在任务进行时才有定时器）
const downloading = computed(() => mod.value?.status === 'downloading')
const now = useNow(downloading)
const showOverride = computed(() => mod.value?.status === 'extracted' && hits.value.length > 0 && !overridden.value)

// 字数落在「按 3 字节估算会超、按字数不会超」的区间时，必须看到全文才能确定是否超过 500KB
const sizeUnknown = computed(() => !!selected.value && loadedBytes.value === undefined && bytesAmbiguous(selected.value))

const blockReason = computed(() => {
  const base = compileBlockReason(mod.value, selected.value, loadedBytes.value)
  if (base) return base
  if (sizeUnknown.value) return textError.value ? '无法确认正文大小：' + textError.value : '正在确认正文大小…'
  return null
})

const display = computed(() => {
  const d = selected.value
  if (!d) return { segments: [], clipped: false, isPreview: true }
  const full = showFull.value ? texts[d.id] : undefined
  const clip = clipForDisplay(full ?? d.preview ?? '')
  return { segments: splitByHits(clip.text, d.license_hits), clipped: clip.clipped, isPreview: full === undefined }
})

function isCurrent(id: number): boolean {
  return detail.value?.module.id === id
}

function resetState() {
  detail.value = null
  selectedId.value = null
  loadError.value = ''
  textError.value = ''
  showFull.value = false
  loadingDocId.value = null
  compileName.value = ''
  logs.value = []
  for (const k of Object.keys(texts)) delete texts[Number(k)]
  for (const k of Object.keys(byteCounts)) delete byteCounts[Number(k)]
}

// 重新上传会替换全部文档（id 全变），缓存里不再存在的文档要清掉
function applyDetail(d: OnlineModuleDetail, keepSelection: boolean) {
  const documents = d.documents || []
  detail.value = { module: d.module, documents }
  const keep = keepSelection && documents.some((x) => x.id === selectedId.value)
  if (!keep) {
    selectedId.value = defaultDocument(documents)?.id ?? null
    showFull.value = false
    textError.value = ''
  }
  const alive = new Set(documents.map((x) => x.id))
  for (const k of Object.keys(texts)) if (!alive.has(Number(k))) delete texts[Number(k)]
  for (const k of Object.keys(byteCounts)) if (!alive.has(Number(k))) delete byteCounts[Number(k)]
}

async function loadDetail(id: number, keepSelection: boolean): Promise<void> {
  const seq = ++loadSeq
  const d = await api<OnlineModuleDetail>('GET', `${BASE}/${id}`)
  if (seq !== loadSeq) return
  applyDetail(d, keepSelection)
}

async function openDetail(id: number) {
  resetState()
  loading.value = true
  const seq = loadSeq + 1
  try {
    await loadDetail(id, false)
  } catch (e) {
    if (seq === loadSeq) loadError.value = errorMessage(e)
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

async function refresh() {
  const m = mod.value
  if (!m) return
  try {
    await loadDetail(m.id, true)
    if (detail.value) emit('changed', detail.value.module)
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  }
}

watch(
  () => [props.open, props.moduleId] as const,
  ([open, id]) => {
    controller?.abort()
    loadSeq++
    if (open && id != null) void openDetail(id)
    else loading.value = false
  },
  { immediate: true },
)

async function loadText(docId: number): Promise<void> {
  const m = mod.value
  if (!m || texts[docId] !== undefined || loadingDocId.value === docId) return
  loadingDocId.value = docId
  textError.value = ''
  try {
    const full = await api<OnlineModuleDocumentFull>('GET', `${BASE}/${m.id}/documents/${docId}`)
    if (!isCurrent(m.id)) return
    const text = full.text ?? ''
    texts[docId] = text
    byteCounts[docId] = utf8Bytes(text)
  } catch (e) {
    if (isCurrent(m.id)) textError.value = errorMessage(e)
  } finally {
    if (loadingDocId.value === docId) loadingDocId.value = null
  }
}

watch(
  selected,
  (d) => {
    textError.value = ''
    if (d && bytesAmbiguous(d)) void loadText(d.id)
  },
  { immediate: true },
)

async function toggleFull() {
  const d = selected.value
  if (!d) return
  if (showFull.value) {
    showFull.value = false
    return
  }
  showFull.value = true
  await loadText(d.id)
}

function select(id: number) {
  if (id === selectedId.value) return
  selectedId.value = id
  showFull.value = false
}

async function override() {
  const m = mod.value
  const d = selected.value
  if (!m || !d || overriding.value) return
  const ok = await ui.confirm(
    `「${d.path}」命中了版权声明筛查（${d.license_hits.join('、')}）。你确认已阅读原文并认为可以编译？放行会记录你的账号和时间。`,
    { danger: true, confirmText: '放行' },
  )
  if (!ok) return
  overriding.value = true
  try {
    const updated = await api<OnlineModule>('POST', `${BASE}/${m.id}/license-override`)
    if (isCurrent(m.id) && detail.value) detail.value.module = updated
    emit('changed', updated)
    ui.toast('已放行')
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  } finally {
    overriding.value = false
  }
}

// NOTE: 自动下载失败后的兜底：管理员手动下载后上传。服务端校验通过就返回 202 和模组记录，提取在后台跑，
// 进度和结果经 WebSocket 推送，所以这里不能当详情用，等任务结束再重新加载详情
async function upload() {
  const m = mod.value
  if (!m || uploading.value) return
  const file = fileInput.value?.files?.[0]
  if (!file) {
    ui.toast('请选择一个 .zip / .docx / .pdf 文件', 'error')
    return
  }
  const invalid = validateArchiveFile(file)
  if (invalid) {
    ui.toast(invalid, 'error')
    return
  }
  uploading.value = true
  try {
    const form = new FormData()
    form.append('file', file)
    const { status, data } = await postForm(`${BASE}/${m.id}/archive`, form)
    if (status < 200 || status >= 300) {
      ui.toast(data?.error || `上传失败（HTTP ${status}）`, 'error')
      return
    }
    const next = data as OnlineModule
    if (fileInput.value) fileInput.value.value = ''
    // NOTE: 任务很快时，结束帧可能先于 202 响应到达；此时 store 里已经是更新的非 downloading 状态，
    // 再套用 202 里的 downloading 会让弹窗永远停在「处理中」，所以直接按已结束处理
    const latest = tasks.modules[m.id]
    if (latest && latest.status !== 'downloading' && Date.parse(latest.updated_at) >= Date.parse(next.updated_at)) {
      void reloadAfterSettle({ ...latest })
    } else if (isCurrent(m.id) && detail.value) {
      detail.value.module = next
    }
    emit('changed', next)
    ui.toast('已开始提取，完成后会通知你')
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  } finally {
    uploading.value = false
  }
}

// NOTE: 后台任务结束后重新加载详情（文档已被替换），保留选中项；加载失败时至少把模组状态更新到最新
async function reloadAfterSettle(module: OnlineModule) {
  try {
    await loadDetail(module.id, true)
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
    if (isCurrent(module.id) && detail.value) detail.value.module = module
  }
  if (isCurrent(module.id) && detail.value) emit('changed', detail.value.module)
}

// NOTE: 结束判断用弹窗自己当前持有的状态，不用 store 的 settled：上传返回 202 时弹窗已经拿到 downloading 状态，
// 即使 store 没见过中间帧（任务秒结束），也能识别「离开 downloading」并重新加载。
// 存入前浅拷贝：编译完成后会就地改 scenario_id，不能连带改到 store 里共享的对象
const offChange = tasks.onChange(({ module }) => {
  const cur = detail.value?.module
  if (!props.open || !detail.value || !cur || cur.id !== module.id) return
  if (cur.status === 'downloading' && module.status !== 'downloading') void reloadAfterSettle({ ...module })
  else detail.value.module = { ...module }
})

async function refreshAfterCompile(id: number, scenarioId: number) {
  try {
    await loadDetail(id, true)
  } catch {
    // 刷新失败不影响编译结果，下面用 done 事件里的 id 兜底
  }
  if (!isCurrent(id) || !detail.value) return
  if (detail.value.module.scenario_id == null) detail.value.module.scenario_id = scenarioId
  emit('changed', detail.value.module)
}

// 离开弹窗即中断在途编译流；后端会继续编译并入库，只是这里不再接收进度
onBeforeUnmount(() => {
  offChange()
  controller?.abort()
})

async function compile() {
  const m = mod.value
  const d = selected.value
  if (!m || !d || compiling.value || blockReason.value) return

  compiling.value = true
  logs.value = []
  const log = (line: string) => {
    logs.value.push(formatLogLine(line))
  }
  log('已提交编译请求，等待服务器响应…')

  const ctrl = new AbortController()
  controller = ctrl
  const handler = createCompileHandler(log)
  const name = compileName.value.trim()
  try {
    await streamSSE(`${BASE}/${m.id}/compile`, name ? { document_id: d.id, name } : { document_id: d.id }, handler.onEvent, {
      signal: ctrl.signal,
    })
    if (ctrl.signal.aborted) return
    const outcome = summarizeCompile(handler.done, handler.error)
    outcome.logs.forEach(log)
    ui.toast(outcome.toast.message, outcome.toast.type)
    const scenarioId = handler.done?.scenario_id
    if (scenarioId != null) await refreshAfterCompile(m.id, scenarioId)
  } catch (e) {
    if (ctrl.signal.aborted) return
    const msg = errorMessage(e)
    log(msg || '编译失败')
    ui.toast(msg, 'error')
  } finally {
    if (controller === ctrl) controller = null
    compiling.value = false
  }
}
</script>

<template>
  <BaseModal :open="open" :title="mod?.title || '线上模组详情'" max-width="max-w-3xl" @close="emit('close')">
    <div v-if="loading" class="space-y-3" aria-busy="true">
      <Skeleton class="h-24" />
      <Skeleton class="h-32" />
    </div>
    <p v-else-if="loadError" class="text-sm text-danger" role="alert">加载失败：{{ loadError }}</p>

    <div v-else-if="mod" class="space-y-6">
      <section class="space-y-3">
        <div class="flex flex-wrap items-center gap-2">
          <StampBadge :tone="statusTone(mod.status, mod.scenario_id)">{{ statusLabel(mod.status, mod.scenario_id) }}</StampBadge>
          <span v-if="mod.scenario_id != null" class="text-sm tabular-nums text-fg">已编译为模组 #{{ mod.scenario_id }}</span>
          <a
            v-if="shareUrl"
            :href="shareUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1 text-sm text-accent hover:text-accent-strong hover:underline"
          >
            <ExternalLink :size="14" :stroke-width="1.75" />
            打开分享页
          </a>
          <BaseButton variant="ghost" size="sm" class="ml-auto" @click="refresh">
            <RefreshCw :size="16" :stroke-width="1.75" />
            刷新
          </BaseButton>
        </div>

        <div class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          <span class="text-fg-subtle">作者</span>
          <span class="break-words text-fg">{{ mod.author || '未知' }}</span>
          <template v-if="metaLine">
            <span class="text-fg-subtle">信息</span>
            <span class="break-words tabular-nums text-fg">{{ metaLine }}</span>
          </template>
          <template v-if="mod.archive_name">
            <span class="text-fg-subtle">压缩包</span>
            <span class="break-words tabular-nums text-fg">{{ mod.archive_name }}（{{ formatBytes(mod.archive_size) }}）</span>
          </template>
          <template v-if="mod.archive_sha256">
            <span class="text-fg-subtle">SHA-256</span>
            <span class="font-mono text-xs break-all text-fg-muted">{{ mod.archive_sha256 }}</span>
          </template>
          <span class="text-fg-subtle">更新时间</span>
          <span class="tabular-nums text-fg">{{ formatTime(mod.updated_at) }}</span>
        </div>

        <p v-if="opinion" class="rounded-lg bg-surface-2 px-3 py-2 text-xs break-words text-fg-muted">{{ opinion }}</p>

        <div v-if="mod.status === 'failed'" class="rounded-lg border border-danger/40 bg-danger/10 p-3" role="alert">
          <p class="flex items-center gap-1.5 text-sm text-fg">
            <TriangleAlert :size="16" :stroke-width="1.75" class="shrink-0 text-danger" />
            {{ DOWNLOAD_FAILED_HINT }}
          </p>
          <p v-if="mod.error" class="mt-1 text-xs break-words text-fg-muted">{{ mod.error }}</p>
        </div>
        <OnlineTaskProgress v-else-if="mod.status === 'downloading'" :module="mod" :now="now" />
        <p v-if="mod.error && mod.status !== 'failed'" class="text-sm break-words text-danger" role="alert">
          最近一次任务失败：{{ mod.error }}
        </p>
      </section>

      <section v-if="docs.length > 0" class="space-y-3">
        <SectionHeading :count="docs.length">提取出的文档</SectionHeading>
        <ul class="max-h-56 space-y-1.5 overflow-y-auto" aria-label="文档列表">
          <li v-for="d in docs" :key="d.id">
            <button
              type="button"
              :aria-pressed="d.id === selectedId"
              class="flex w-full flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-left transition"
              :class="d.id === selectedId ? 'border-accent bg-surface-3' : 'border-transparent bg-surface-2 hover:bg-surface-3'"
              @click="select(d.id)"
            >
              <FileText :size="16" :stroke-width="1.75" class="shrink-0 text-fg-subtle" />
              <span class="min-w-0 flex-1 truncate text-sm text-fg">{{ d.path }}</span>
              <StampBadge>{{ d.kind }}</StampBadge>
              <span class="text-xs tabular-nums text-fg-muted">{{ d.runes }} 字</span>
              <StampBadge v-if="d.candidate" tone="seal">候选正文</StampBadge>
              <StampBadge v-if="d.ocr" tone="ledger">OCR 识别</StampBadge>
              <StampBadge v-if="d.garbled" tone="danger">疑似乱码</StampBadge>
              <StampBadge v-if="d.license_hits.length > 0" tone="danger">命中版权筛查</StampBadge>
            </button>
          </li>
        </ul>

        <div v-if="selected" class="space-y-2">
          <div class="flex items-center justify-between gap-2">
            <span class="text-sm text-fg-muted">{{ display.isPreview ? '预览' : '全文' }}</span>
            <BaseButton
              v-if="selected.runes > PREVIEW_RUNES"
              variant="ghost"
              size="sm"
              :loading="textLoading"
              @click="toggleFull"
            >
              {{ showFull ? '收起全文' : '查看全文' }}
            </BaseButton>
          </div>
          <p v-if="showFull && textError" class="text-xs text-danger" role="alert">全文加载失败：{{ textError }}</p>
          <div
            class="max-h-72 overflow-y-auto rounded-lg bg-bg p-3 text-sm leading-relaxed break-words whitespace-pre-wrap text-fg-muted"
            data-testid="doc-text"
          >
            <template v-for="(seg, i) in display.segments" :key="i">
              <mark v-if="seg.hit" class="rounded-md bg-warning/30 px-0.5 text-fg">{{ seg.text }}</mark>
              <template v-else>{{ seg.text }}</template>
            </template>
            <span v-if="display.segments.length === 0" class="text-fg-subtle">（没有可预览的内容）</span>
          </div>
          <p v-if="display.clipped" class="text-xs text-fg-subtle">全文过长，这里只显示前 20 万字；编译使用完整正文。</p>
        </div>
      </section>

      <section
        v-if="selected && hits.length > 0"
        class="rounded-lg border border-warning/40 bg-warning/10 p-3"
        :aria-label="'版权命中片段'"
      >
        <p class="flex items-center gap-1.5 text-sm font-medium text-fg">
          <ShieldAlert :size="16" :stroke-width="1.75" class="shrink-0 text-warning" />
          所选文档命中了版权声明筛查
        </p>
        <p class="mt-1 text-xs text-fg-muted">下面是命中的片段，请先阅读原文确认作者是否允许 AI 改编，再决定是否放行。</p>
        <ul class="mt-2 flex flex-wrap gap-2">
          <li v-for="(h, i) in hits" :key="i" class="rounded-md bg-surface-3 px-2 py-1 text-xs break-words text-fg">{{ h }}</li>
        </ul>
        <div class="mt-3 flex flex-wrap items-center gap-3">
          <BaseButton v-if="showOverride" variant="danger" size="sm" :loading="overriding" @click="override">
            <ShieldCheck :size="16" :stroke-width="1.75" />
            放行
          </BaseButton>
          <p v-else-if="overridden" class="flex items-center gap-1.5 text-sm text-success">
            <ShieldCheck :size="16" :stroke-width="1.75" />
            已于 {{ formatTime(mod.license_override_at) }} 放行
          </p>
        </div>
      </section>

      <section class="space-y-2">
        <SectionHeading>手动上传</SectionHeading>
        <p class="text-xs text-fg-subtle">
          自动下载失败，或想换一份文件时，可以在自己的浏览器里下载压缩包后上传（.zip / .docx / .pdf，≤50MB）。上传会替换已提取的文档，并清除放行记录。
        </p>
        <div class="flex flex-wrap items-center gap-2">
          <input
            ref="fileInput"
            type="file"
            accept=".zip,.docx,.pdf"
            aria-label="选择要上传的压缩包"
            class="block min-w-0 flex-1 rounded-lg border border-line bg-surface-2 px-3 py-2 text-sm text-fg file:mr-3 file:rounded-lg file:border-0 file:bg-surface-3 file:px-2 file:py-1 file:text-xs file:text-fg"
          />
          <BaseButton variant="secondary" :loading="uploading" :disabled="compiling || downloading" @click="upload">
            <Upload v-if="!uploading" :size="16" :stroke-width="1.75" />
            {{ uploading ? '上传中…' : '上传' }}
          </BaseButton>
        </div>
      </section>

      <section class="space-y-2">
        <SectionHeading>编译为模组</SectionHeading>
        <FormField label="模组名称（可选）">
          <BaseInput v-model="compileName" type="text" :placeholder="'留空使用「' + mod.title + '」'" />
        </FormField>
        <div class="flex flex-wrap items-center gap-3">
          <BaseButton :loading="compiling" :disabled="!!blockReason" @click="compile">
            <Hammer v-if="!compiling" :size="16" :stroke-width="1.75" />
            {{ compiling ? '编译中…' : mod.scenario_id != null ? '重新编译' : '编译' }}
          </BaseButton>
          <p v-if="blockReason" class="text-sm text-fg-muted" data-testid="compile-block-reason">{{ blockReason }}</p>
        </div>
        <StreamLogBox v-if="compiling || logs.length" title="编译进度" :lines="logs" :running="compiling" @clear="logs = []" />
      </section>
    </div>
  </BaseModal>
</template>
