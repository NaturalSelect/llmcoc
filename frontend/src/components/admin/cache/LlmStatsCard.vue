<script setup lang="ts">
import { RefreshCw, Trash2 } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import type { LlmStats } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { errMsg } from '@/components/admin/shared/errMsg'
import { useUiStore } from '@/stores/ui'
import { llmLineText, llmOverallView } from './cacheFormat'

const ui = useUiStore()

const stats = ref<LlmStats | null>(null)

async function load() {
  try {
    stats.value = await api<LlmStats>('GET', '/api/admin/llm/stats')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

async function reset() {
  if (!(await ui.confirm('确认清空 LLM 调用延迟统计？', { danger: true, confirmText: '清空' }))) return
  try {
    await api('DELETE', '/api/admin/llm/stats')
    ui.toast('LLM 延迟统计已清空')
    await load()
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

onMounted(load)
</script>

<template>
  <PaperCard tag="section" aria-labelledby="llm-stats-title" class="max-w-xl">
    <div class="mb-2 flex items-center justify-between gap-3">
      <SectionHeading id="llm-stats-title">LLM 调用统计</SectionHeading>
      <div class="flex gap-2">
        <BaseButton variant="ghost" size="sm" @click="load"><RefreshCw :size="16" :stroke-width="1.75" />刷新</BaseButton>
        <BaseButton variant="danger" size="sm" @click="reset"><Trash2 :size="16" :stroke-width="1.75" />清空统计</BaseButton>
      </div>
    </div>
    <p class="mb-4 text-xs leading-relaxed text-fg-subtle">
      持续累积的历史平均值，按调用方角色/模型聚合；与上方 Provider 连通性测试返回的单次探活延迟是两回事。缓存命中率 = cache_read_tokens / prompt_tokens。
    </p>

    <p v-if="!stats" class="text-sm text-fg-subtle" role="status">加载中…</p>
    <div v-else class="space-y-4">
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div class="rounded-lg bg-surface-2 p-3">
          <div class="text-xs text-fg-subtle">总调用数</div>
          <div class="mt-1 text-lg tabular-nums text-fg">{{ llmOverallView(stats).count }}</div>
        </div>
        <div class="rounded-lg bg-surface-2 p-3">
          <div class="text-xs text-fg-subtle">平均延迟</div>
          <div class="mt-1 text-lg tabular-nums text-fg">{{ llmOverallView(stats).avg }}</div>
        </div>
        <div class="rounded-lg bg-surface-2 p-3">
          <div class="text-xs text-fg-subtle">最大延迟</div>
          <div class="mt-1 text-lg tabular-nums text-fg">{{ llmOverallView(stats).max }}</div>
        </div>
        <div class="rounded-lg bg-surface-2 p-3">
          <div class="text-xs text-fg-subtle">错误数</div>
          <div class="mt-1 text-lg tabular-nums text-fg">{{ llmOverallView(stats).errors }}</div>
        </div>
      </div>
      <div class="rounded-lg bg-surface-2 p-3">
        <div class="text-xs text-fg-subtle">整体缓存命中率</div>
        <div class="mt-1 text-lg tabular-nums text-fg">{{ llmOverallView(stats).hitRate }}</div>
      </div>

      <div v-if="stats.by_role?.length" class="border-t border-line pt-3">
        <div class="mb-2 text-xs text-fg-subtle">按角色</div>
        <ul class="space-y-1.5">
          <li v-for="line in stats.by_role" :key="'role-' + line.key" class="flex flex-wrap items-center justify-between gap-x-3 text-sm text-fg-muted">
            <span>{{ line.key }}</span>
            <span class="text-xs tabular-nums">{{ llmLineText(line) }}</span>
          </li>
        </ul>
      </div>
      <div v-if="stats.by_model?.length" class="border-t border-line pt-3">
        <div class="mb-2 text-xs text-fg-subtle">按模型</div>
        <ul class="space-y-1.5">
          <li v-for="line in stats.by_model" :key="'model-' + line.key" class="flex flex-wrap items-center justify-between gap-x-3 text-sm text-fg-muted">
            <span>{{ line.key }}</span>
            <span class="text-xs tabular-nums">{{ llmLineText(line) }}</span>
          </li>
        </ul>
      </div>
    </div>
  </PaperCard>
</template>
