<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { LlmStats } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'
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
    <div class="mb-2 flex items-center justify-between">
      <h3 id="llm-stats-title" class="font-type text-sm font-bold tracking-widest text-seal">LLM 调用统计</h3>
      <div class="flex gap-2">
        <BaseButton variant="ghost" class="!px-3 !py-1 !text-xs" @click="load">刷新</BaseButton>
        <BaseButton variant="danger" class="!px-3 !py-1 !text-xs" @click="reset">清空统计</BaseButton>
      </div>
    </div>
    <p class="mb-3 text-xs text-ink-faint">
      持续累积的历史平均值，按调用方角色/模型聚合；与上方 Provider 连通性测试返回的单次探活延迟是两回事。缓存命中率 = cache_read_tokens / prompt_tokens。
    </p>

    <p v-if="!stats" class="text-sm text-ink-faint" role="status">加载中…</p>
    <div v-else class="space-y-3">
      <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <div class="rounded-sm border border-paper-edge bg-paper-deep/50 p-2 text-center">
          <div class="font-type text-lg font-bold text-ink">{{ llmOverallView(stats).count }}</div>
          <div class="mt-0.5 font-type text-[10px] text-ink-faint">总调用数</div>
        </div>
        <div class="rounded-sm border border-paper-edge bg-paper-deep/50 p-2 text-center">
          <div class="font-type text-lg font-bold text-ink">{{ llmOverallView(stats).avg }}</div>
          <div class="mt-0.5 font-type text-[10px] text-ink-faint">平均延迟</div>
        </div>
        <div class="rounded-sm border border-paper-edge bg-paper-deep/50 p-2 text-center">
          <div class="font-type text-lg font-bold text-ink">{{ llmOverallView(stats).max }}</div>
          <div class="mt-0.5 font-type text-[10px] text-ink-faint">最大延迟</div>
        </div>
        <div class="rounded-sm border border-paper-edge bg-paper-deep/50 p-2 text-center">
          <div class="font-type text-lg font-bold text-seal">{{ llmOverallView(stats).errors }}</div>
          <div class="mt-0.5 font-type text-[10px] text-ink-faint">错误数</div>
        </div>
      </div>
      <div class="rounded-sm border border-paper-edge bg-paper-deep/50 p-2 text-center">
        <div class="font-type text-lg font-bold text-ink">{{ llmOverallView(stats).hitRate }}</div>
        <div class="mt-0.5 font-type text-[10px] text-ink-faint">整体缓存命中率</div>
      </div>

      <div v-if="stats.by_role?.length" class="border-t border-paper-edge pt-2">
        <div class="mb-1 font-type text-[10px] font-bold uppercase tracking-wider text-ink-soft">按角色</div>
        <ul class="space-y-1">
          <li v-for="line in stats.by_role" :key="'role-' + line.key" class="flex flex-wrap items-center justify-between gap-x-3 text-xs text-ink-soft">
            <span>{{ line.key }}</span>
            <span>{{ llmLineText(line) }}</span>
          </li>
        </ul>
      </div>
      <div v-if="stats.by_model?.length" class="border-t border-paper-edge pt-2">
        <div class="mb-1 font-type text-[10px] font-bold uppercase tracking-wider text-ink-soft">按模型</div>
        <ul class="space-y-1">
          <li v-for="line in stats.by_model" :key="'model-' + line.key" class="flex flex-wrap items-center justify-between gap-x-3 text-xs text-ink-soft">
            <span>{{ line.key }}</span>
            <span>{{ llmLineText(line) }}</span>
          </li>
        </ul>
      </div>
    </div>
  </PaperCard>
</template>
