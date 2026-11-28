<script setup lang="ts">
import { RouterLink } from 'vue-router'
import type { ClassifierLog } from '@/api/classifierTypes'
import { barPercent, executionNote, fmt2, fmtDateTime, fmtElapsed, isHit, optionLabel, probabilityRows, requestEntries, verdictText } from '@/game/classifier'

// NOTE: 管理员专用：一条分类器记录的详细结果（各选项概率、分数与阈值、错误原因、执行的 Director、发给分类器的请求）。
// 记录里的文本来自玩家和 KP，全部只做文本插值，不能用 v-html
defineProps<{ log: ClassifierLog }>()
</script>

<template>
  <div class="space-y-4 rounded-lg border border-line bg-surface-2 px-3 py-3" data-testid="classifier-detail">
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
      <template v-if="log.subject">
        <dt class="text-fg-subtle">调查员</dt>
        <dd class="break-words text-fg" data-testid="detail-subject">{{ log.subject }}</dd>
      </template>
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

    <p class="text-xs text-fg-subtle" data-testid="detail-settings-link">
      调整阈值：
      <RouterLink to="/admin/settings" target="_blank" rel="noopener" class="text-info underline hover:text-fg">管理 → 系统设置</RouterLink>
    </p>
  </div>
</template>
