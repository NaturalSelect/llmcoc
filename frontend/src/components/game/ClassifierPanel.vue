<script setup lang="ts">
import { ChevronDown, ChevronRight, RefreshCw } from '@lucide/vue'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { classifierApi } from '@/api/classifierApi'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import TabBar from '@/components/base/TabBar.vue'
import {
  barPercent,
  CLASSIFIER_FILTERS,
  decisionLabel,
  decisionTone,
  executionNote,
  fmt2,
  fmtClock,
  fmtDateTime,
  fmtElapsed,
  fmtScore,
  isHit,
  kindLabel,
  mergeClassifierLogs,
  optionLabel,
  probabilityRows,
  requestEntries,
  verdictText,
  visibleLogs,
} from '@/game/classifier'
import type { Room } from '@/game/roomState'
import { useAuthStore } from '@/stores/auth'

// NOTE: 管理员专用：两个 System One 分类器（输入分类、NSFW 路由）每一次的详细结果。记录里的文本来自玩家和 KP，全部只做文本插值，不能用 v-html
const props = defineProps<{ open: boolean; sessionId: number | null; room: Room | null }>()
const emit = defineEmits<{ close: [] }>()

const auth = useAuthStore()
const filter = ref('all')
const loading = ref(false)
const loadError = ref('')
const expanded = reactive(new Set<number>())
// NOTE: 只认最近一次请求；房间切换后迟到的响应不能并进新房间
let loadGen = 0

const logs = computed(() => visibleLogs(props.room?.classifierLogs ?? [], filter.value))
const total = computed(() => props.room?.classifierLogs.length ?? 0)

async function load() {
  const room = props.room
  const sid = props.sessionId
  // NOTE: 面板只在管理员界面里挂载，这里再兜一层：非管理员绝不发起这个请求
  if (!auth.isAdmin || !room || !sid) return
  const gen = ++loadGen
  loading.value = true
  loadError.value = ''
  try {
    const history = await classifierApi.list(sid)
    if (gen !== loadGen || props.room !== room) return
    mergeClassifierLogs(room.classifierLogs, history)
  } catch (e) {
    if (gen === loadGen) loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    if (gen === loadGen) loading.value = false
  }
}

function toggle(id: number) {
  if (expanded.has(id)) expanded.delete(id)
  else expanded.add(id)
}

// NOTE: 只在打开时请求一次历史，之后靠 room 里实时并入的 classifier_log 事件更新；不轮询
watch(
  () => props.open,
  (open) => {
    if (open) void load()
    else expanded.clear()
  },
  { immediate: true },
)
watch(
  () => props.room,
  () => {
    expanded.clear()
    loadError.value = ''
    if (props.open) void load()
  },
)
</script>

<template>
  <BaseModal :open="open" title="分类器记录" max-width="max-w-3xl" @close="emit('close')">
    <div class="space-y-3">
      <p class="text-xs text-fg-subtle">
        阈值在
        <RouterLink to="/admin/settings" target="_blank" class="text-info underline hover:text-fg">管理 → 系统设置</RouterLink>
        里调整
      </p>

      <div class="flex items-center gap-3">
        <TabBar v-model="filter" class="min-w-0 flex-1" :tabs="[...CLASSIFIER_FILTERS]" />
        <span class="shrink-0 text-xs tabular-nums text-fg-subtle" data-testid="classifier-count">共 {{ total }} 条</span>
        <BaseButton variant="ghost" size="sm" :loading="loading" @click="load">
          <RefreshCw :size="16" :stroke-width="1.75" />
          刷新
        </BaseButton>
      </div>

      <div
        v-if="loadError"
        class="flex flex-wrap items-center gap-3 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning"
        role="alert"
        data-testid="classifier-error"
      >
        <span class="min-w-0 flex-1 break-words">加载历史记录失败：{{ loadError }}。下面只有本次打开后实时收到的记录。</span>
        <BaseButton variant="ghost" size="sm" :disabled="loading" @click="load">重试</BaseButton>
      </div>

      <ul v-if="logs.length" class="space-y-2" aria-label="分类器记录">
        <li v-for="log in logs" :key="log.id" class="rounded-lg border border-line bg-surface-2" data-testid="classifier-row">
          <button
            type="button"
            class="flex w-full flex-wrap items-center gap-x-3 gap-y-1 rounded-lg px-3 py-2 text-left text-sm transition hover:bg-surface-3 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            :aria-expanded="expanded.has(log.id)"
            :aria-controls="`classifier-detail-${log.id}`"
            @click="toggle(log.id)"
          >
            <component :is="expanded.has(log.id) ? ChevronDown : ChevronRight" :size="16" :stroke-width="1.75" class="shrink-0 text-fg-subtle" />
            <span class="shrink-0 font-mono text-xs tabular-nums text-fg-subtle" data-testid="row-time">{{ fmtClock(log.created_at) }}</span>
            <StampBadge class="shrink-0" data-testid="row-kind">{{ kindLabel(log.kind) }}</StampBadge>
            <span class="min-w-0 flex-1 truncate text-fg" :title="log.subject" data-testid="row-subject">{{ log.subject || '—' }}</span>
            <StampBadge class="shrink-0" :tone="decisionTone(log)" data-testid="row-decision">{{ decisionLabel(log) }}</StampBadge>
            <span class="shrink-0 text-xs tabular-nums text-fg-muted" data-testid="row-score">{{ fmtScore(log) }}</span>
            <span class="shrink-0 text-xs tabular-nums text-fg-subtle" data-testid="row-elapsed">{{ fmtElapsed(log.elapsed_ms) }}</span>
          </button>

          <div v-if="expanded.has(log.id)" :id="`classifier-detail-${log.id}`" class="space-y-4 border-t border-line px-3 py-3" data-testid="classifier-detail">
            <p
              v-if="log.status === 'error' || log.error"
              class="rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-xs break-words text-warning"
              role="note"
              data-testid="detail-error"
            >
              错误原因：{{ log.error || '未提供原因' }}
            </p>

            <section aria-label="各选项概率">
              <h3 class="mb-2 text-xs font-medium text-fg-subtle">各选项概率</h3>
              <ul v-if="probabilityRows(log.probabilities, log.choice).length" class="space-y-1.5">
                <li
                  v-for="r in probabilityRows(log.probabilities, log.choice)"
                  :key="r.name"
                  class="flex items-center gap-2 text-xs"
                  data-testid="prob-row"
                  :data-top="r.top"
                >
                  <span class="w-24 shrink-0 text-right" :class="r.top ? 'font-medium text-accent' : 'text-fg-muted'" :title="r.name">{{ r.label }}</span>
                  <div
                    class="h-2 min-w-0 flex-1 overflow-hidden rounded-full bg-surface-3"
                    role="progressbar"
                    aria-valuemin="0"
                    aria-valuemax="100"
                    :aria-valuenow="r.percent"
                    :aria-label="r.label"
                  >
                    <div class="h-full rounded-full" :class="r.top ? 'bg-accent' : 'bg-line-strong'" :style="{ width: r.percent + '%' }" />
                  </div>
                  <span class="w-14 shrink-0 text-right tabular-nums" :class="r.top ? 'text-fg' : 'text-fg-muted'">{{ r.text }}</span>
                  <span class="w-8 shrink-0 text-accent">{{ r.top ? '最高' : '' }}</span>
                </li>
              </ul>
              <p v-else class="text-xs text-fg-subtle">没有概率数据。</p>
            </section>

            <section aria-label="分数与阈值">
              <h3 class="mb-2 text-xs font-medium text-fg-subtle">分数与阈值</h3>
              <div v-if="log.status !== 'error'" class="relative">
                <div class="h-2 overflow-hidden rounded-full bg-surface-3">
                  <div class="h-full rounded-full" :class="isHit(log) ? 'bg-accent' : 'bg-fg-subtle'" :style="{ width: barPercent(log.score) + '%' }" />
                </div>
                <div class="absolute -top-1 h-4 w-0.5 bg-fg" :style="{ left: barPercent(log.threshold) + '%' }" aria-hidden="true" data-testid="threshold-mark" />
              </div>
              <p class="mt-1.5 text-xs tabular-nums text-fg-muted" data-testid="detail-verdict">
                <template v-if="log.status !== 'error'">分数 {{ fmt2(log.score) }} · </template>
                阈值 {{ fmt2(log.threshold) }} · {{ verdictText(log) }}
              </p>
            </section>

            <p v-if="executionNote(log)" class="text-xs text-fg" data-testid="detail-role">{{ executionNote(log) }}</p>

            <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
              <template v-if="log.choice">
                <dt class="text-fg-subtle">最高项</dt>
                <dd class="break-words text-fg">{{ optionLabel(log.choice) }}</dd>
              </template>
              <dt class="text-fg-subtle">时间</dt>
              <dd class="tabular-nums text-fg">{{ fmtDateTime(log.created_at) || '—' }}</dd>
              <template v-if="log.floor > 0">
                <dt class="text-fg-subtle">周期</dt>
                <dd class="tabular-nums text-fg" data-testid="detail-floor">{{ log.floor }}</dd>
              </template>
              <template v-if="log.user_id > 0">
                <dt class="text-fg-subtle">用户 ID</dt>
                <dd class="tabular-nums text-fg">{{ log.user_id }}</dd>
              </template>
              <dt class="text-fg-subtle">耗时</dt>
              <dd class="tabular-nums text-fg">{{ fmtElapsed(log.elapsed_ms) }}</dd>
              <template v-if="log.request_id">
                <dt class="text-fg-subtle">请求 ID</dt>
                <dd class="font-mono break-all text-fg" data-testid="detail-request-id">{{ log.request_id }}</dd>
              </template>
            </dl>

            <section v-if="requestEntries(log.request).length" aria-label="发给分类器的请求">
              <h3 class="mb-1 text-xs font-medium text-fg-subtle">发给分类器的请求</h3>
              <div v-for="e in requestEntries(log.request)" :key="e.key" class="mt-2" data-testid="request-entry">
                <p class="font-mono text-xs text-fg-subtle">{{ e.key }}</p>
                <pre class="mt-1 max-h-48 overflow-auto rounded-lg bg-surface px-3 py-2 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap text-fg-muted" data-testid="request-value">{{ e.value || '（空）' }}</pre>
              </div>
            </section>
          </div>
        </li>
      </ul>

      <p v-else-if="loading" class="py-8 text-center text-sm text-fg-subtle">加载中…</p>
      <EmptyState v-else-if="total === 0">暂无分类记录：玩家提交行动、KP 周期开始前，分类器每次判断的详情都会记在这里并实时更新。</EmptyState>
      <EmptyState v-else>这个分类下暂无记录。</EmptyState>
    </div>
  </BaseModal>
</template>
