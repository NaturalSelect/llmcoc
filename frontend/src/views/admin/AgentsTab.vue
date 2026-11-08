<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { AgentConfigRow, AgentFormState, ProviderConfig } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import AgentCard from '@/components/admin/agents/AgentCard.vue'
import { buildAgentForms } from '@/components/admin/agents/agentRoles'
import { errMsg } from '@/components/admin/shared/errMsg'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()

const forms = ref<AgentFormState[]>([])
const providers = ref<ProviderConfig[]>([])
// NOTE: 刷新时自增，作为卡片 key 的一部分，让卡片重建并丢弃未保存的本地修改
const generation = ref(0)

async function loadAgents() {
  const rows = await api<AgentConfigRow[] | null>('GET', '/api/admin/config/agents')
  forms.value = buildAgentForms(rows)
  generation.value += 1
}

async function loadProviders() {
  providers.value = (await api<ProviderConfig[] | null>('GET', '/api/admin/config/providers')) || []
}

async function refresh() {
  try {
    await loadAgents()
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

// NOTE: 旧前端进入后台时提供商与 Agent 各自独立加载并各自提示错误，这里保持一致
onMounted(() => {
  loadProviders().catch((e) => ui.toast(errMsg(e), 'error'))
  void refresh()
})
</script>

<template>
  <div class="space-y-3">
    <div class="flex items-center justify-between">
      <h3 class="font-type text-sm font-bold tracking-widest text-seal">Agent 配置</h3>
      <button type="button" class="font-type text-xs text-ink-faint transition hover:text-seal" @click="refresh">刷新</button>
    </div>
    <p class="text-xs text-ink-faint">留空的系统提示词将使用内置默认提示词。</p>

    <div class="space-y-3">
      <AgentCard v-for="f in forms" :key="`${generation}-${f.id}`" :initial="f" :providers="providers" />
      <p v-if="forms.length === 0" class="text-sm text-ink-faint" role="status">加载中…</p>
    </div>
  </div>
</template>
