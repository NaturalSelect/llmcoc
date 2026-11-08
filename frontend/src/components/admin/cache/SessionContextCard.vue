<script setup lang="ts">
import { ref } from 'vue'
import type { SessionContext } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import PaperCard from '@/components/base/PaperCard.vue'
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
    <h3 id="session-ctx-title" class="mb-2 font-type text-sm font-bold tracking-widest text-seal">会话上下文</h3>
    <p class="mb-3 text-xs text-ink-faint">
      按会话ID查看每个agent(director/writer/dramaturg/npc:*)已提交的历史轮数、估算字符数与最近若干轮的缓存命中统计，用于核对某个具体会话的prompt cache前缀是否稳定命中。
    </p>
    <div class="mb-3 flex gap-2">
      <BaseInput v-model="sessionId" type="text" placeholder="会话ID" aria-label="会话ID" class="flex-1 !text-sm" @keydown.enter="load" />
      <BaseButton variant="ghost" class="!px-3 !py-1 !text-xs" :disabled="loading" @click="load">查询</BaseButton>
    </div>

    <p v-if="loading" class="text-sm text-ink-faint" role="status">查询中…</p>
    <template v-else-if="context">
      <p v-if="!(context.agents ?? []).length" class="text-sm text-ink-faint">该会话暂无已持久化的上下文记录</p>
      <div v-else class="space-y-3">
        <div v-for="agent in context.agents" :key="agent.agent_key" class="rounded-sm border border-paper-edge bg-paper-deep/50 p-2">
          <div class="mb-1 flex flex-wrap items-center justify-between gap-x-3 text-xs">
            <span class="font-type font-bold text-ink">{{ agent.agent_key }}</span>
            <span class="text-ink-soft">{{ agentSummaryText(agent) }}</span>
          </div>
          <ul class="space-y-0.5">
            <li
              v-for="stat in recentStatsNewestFirst(agent.recent_stats)"
              :key="agent.agent_key + '-' + stat.seq"
              class="flex flex-wrap items-center justify-between gap-x-3 font-type text-[11px] text-ink-faint"
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
