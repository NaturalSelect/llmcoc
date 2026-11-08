<script setup lang="ts">
import { Search } from '@lucide/vue'
import { ref } from 'vue'
import type { SessionContext } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { errMsg } from '@/components/admin/shared/errMsg'
import { useUiStore } from '@/stores/ui'
import { agentSummaryText, recentStatsNewestFirst, turnStatLeft, turnStatRight } from './cacheFormat'

const ui = useUiStore()

const sessionId = ref('')
const context = ref<SessionContext | null>(null)
const loading = ref(false)
// NOTE: 连续查询不同会话时，只采纳最后一次请求的结果
let seq = 0

async function load() {
  const id = String(sessionId.value || '').trim()
  if (!id) {
    ui.toast('请输入会话ID', 'error')
    return
  }
  const mine = ++seq
  loading.value = true
  try {
    const resp = await api<SessionContext>('GET', '/api/admin/sessions/' + encodeURIComponent(id) + '/context')
    if (mine === seq) context.value = resp
  } catch (e) {
    if (mine === seq) {
      context.value = null
      ui.toast('查询会话上下文失败：' + errMsg(e), 'error')
    }
  } finally {
    if (mine === seq) loading.value = false
  }
}
</script>

<template>
  <PaperCard tag="section" aria-labelledby="session-ctx-title" class="max-w-xl">
    <SectionHeading id="session-ctx-title" class="mb-2">会话上下文</SectionHeading>
    <p class="mb-4 text-xs leading-relaxed text-fg-subtle">
      按会话ID查看每个agent(director/writer/dramaturg/npc:*)已提交的历史轮数、估算字符数与最近若干轮的缓存命中统计，用于核对某个具体会话的prompt cache前缀是否稳定命中。
    </p>
    <div class="mb-4 flex gap-2">
      <BaseInput v-model="sessionId" type="text" placeholder="会话ID" aria-label="会话ID" class="flex-1" @keydown.enter="load" />
      <BaseButton variant="ghost" :disabled="loading" @click="load"><Search :size="16" :stroke-width="1.75" />查询</BaseButton>
    </div>

    <p v-if="loading" class="text-sm text-fg-subtle" role="status">查询中…</p>
    <template v-else-if="context">
      <p v-if="!(context.agents ?? []).length" class="text-sm text-fg-subtle">该会话暂无已持久化的上下文记录</p>
      <div v-else class="space-y-3">
        <div v-for="agent in context.agents" :key="agent.agent_key" class="rounded-lg bg-surface-2 p-3">
          <div class="mb-2 flex flex-wrap items-center justify-between gap-x-3 text-sm">
            <span class="font-mono text-fg">{{ agent.agent_key }}</span>
            <span class="text-xs tabular-nums text-fg-muted">{{ agentSummaryText(agent) }}</span>
          </div>
          <ul class="space-y-1">
            <li
              v-for="stat in recentStatsNewestFirst(agent.recent_stats)"
              :key="agent.agent_key + '-' + stat.seq"
              class="flex flex-wrap items-center justify-between gap-x-3 font-mono text-xs tabular-nums text-fg-subtle"
            >
              <span>{{ turnStatLeft(stat) }}</span>
              <span>{{ turnStatRight(stat) }}</span>
            </li>
          </ul>
        </div>
      </div>
    </template>
  </PaperCard>
</template>
