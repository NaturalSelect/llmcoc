<script setup lang="ts">
import { reactive, ref } from 'vue'
import type { AgentFormState, PingResult, ProviderConfig } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import BaseTextarea from '@/components/base/BaseTextarea.vue'
import FormField from '@/components/base/FormField.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { useUiStore } from '@/stores/ui'
import { errMsg } from '../shared/errMsg'
import {
  buildSavePayload,
  hasContextWindow,
  isPainterRole,
  pingFailureMessage,
  pingLoadingKey,
  pingSuccessMessage,
  planPing,
  savedMessage,
} from './agentActions'
import { agentDefaultHint, agentDesc, agentLabel } from './agentRoles'

const props = defineProps<{ initial: AgentFormState; providers: ProviderConfig[] }>()
const ui = useUiStore()

// NOTE: 卡片持有自己的表单副本，不直接改 props；父组件刷新时用新 key 重建卡片，未保存的修改随之丢弃（与旧前端一致）
const form = reactive<AgentFormState>({ ...props.initial })
const saving = ref(false)
const pingingKey = ref<string | null>(null)

async function save() {
  saving.value = true
  try {
    await api('PUT', '/api/admin/config/agents/' + form.role, buildSavePayload(form))
    ui.toast(savedMessage(form.role))
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    saving.value = false
  }
}

async function ping() {
  const plan = planPing(form)
  if (!plan.ok) {
    ui.toast(plan.error, 'error')
    return
  }
  pingingKey.value = plan.key
  try {
    const r = await api<PingResult>('POST', `/api/admin/config/providers/${plan.providerId}/ping`, plan.payload)
    ui.toast(pingSuccessMessage(plan.role, r.latency_ms, r.dimensions))
  } catch (e) {
    ui.toast(pingFailureMessage(plan.role, errMsg(e)), 'error')
  } finally {
    pingingKey.value = null
  }
}

// NOTE: 判定键含提供商：测试期间换了提供商，按钮立即恢复可点
const pinging = () => pingingKey.value === pingLoadingKey(form)
</script>

<template>
  <PaperCard tag="article" :aria-label="agentLabel(form.role)" class="space-y-4">
    <!-- NOTE: 「启用」复选框必须留在卡片底部：测试按 DOM 顺序取复选框下标（0 关温度、1 走 Chat、2 扩散模式），放到头部会打乱 -->
    <header class="flex flex-wrap items-center gap-x-3 gap-y-1">
      <h4 class="text-base font-semibold text-fg">{{ agentLabel(form.role) }}</h4>
      <span class="text-sm text-fg-subtle">{{ agentDesc(form.role) }}</span>
      <span v-if="form.model_name" class="font-mono text-xs text-fg-muted">{{ form.model_name }}</span>
      <StampBadge v-if="!form.is_active" tone="danger" class="ml-auto">已停用</StampBadge>
    </header>

    <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
      <FormField label="提供商">
        <BaseSelect v-model="form.provider_config_id">
          <option value="">使用全局配置</option>
          <option v-for="p in providers" :key="p.id" :value="p.id">{{ p.name }}</option>
        </BaseSelect>
      </FormField>
      <FormField label="模型名称">
        <BaseInput v-model="form.model_name" placeholder="如 gpt-4o" />
      </FormField>
      <FormField label="MaxTokens">
        <BaseInput v-model.number="form.max_tokens" type="number" />
      </FormField>
      <FormField label="Temp">
        <BaseInput v-model.number="form.temperature" type="number" step="0.1" min="0" max="2" :disabled="form.disable_temperature" />
      </FormField>
      <FormField v-if="hasContextWindow(form.role)" label="上下文窗口（token，按模型实际值填写）">
        <BaseInput
          v-model.number="form.context_window"
          type="number"
          min="0"
          :placeholder="form.role === 'director' ? '0 = 不整体裁剪历史' : '0 = 使用默认 300000'"
        />
      </FormField>
      <FormField label="推理强度">
        <BaseSelect v-model="form.thinking_level">
          <option value="">默认（high）</option>
          <option value="none">关闭</option>
          <option value="low">低</option>
          <option value="medium">中</option>
          <option value="high">高</option>
          <option value="xhigh">极高</option>
          <option value="max">最高</option>
        </BaseSelect>
      </FormField>
      <FormField label="思考预算（token，Anthropic 专用）">
        <BaseInput
          v-model.number="form.thinking_budget_tokens"
          type="number"
          min="0"
          placeholder="0 = 自适应思考；claude-haiku-4-5 等不支持自适应的模型填 ≥1024 且小于 MaxTokens"
        />
      </FormField>
    </div>

    <div class="space-y-2 rounded-lg bg-surface-2 p-3 text-sm text-fg-muted">
      <label class="flex cursor-pointer items-start gap-2">
        <input v-model="form.disable_temperature" type="checkbox" class="mt-1 accent-accent" />
        <span>关闭 Temperature（不支持温度参数的模型，如 o1/o3）</span>
      </label>
      <template v-if="isPainterRole(form.role)">
        <label class="flex cursor-pointer items-start gap-2">
          <input v-model="form.image_via_chat" type="checkbox" class="mt-1 accent-accent" :disabled="form.diffusion_mode" />
          <span>画图走 Chat 接口（部分画图模型/中转网关只支持 /chat/completions 调用）</span>
        </label>
        <label class="flex cursor-pointer items-start gap-2">
          <input v-model="form.diffusion_mode" type="checkbox" class="mt-1 accent-accent" :disabled="form.image_via_chat" />
          <span>
            扩散模型模式（如 new-api 接入的 NovelAI：先由 Diffusion Translator 把画面描述翻译成标签式提示词（负面提示词为固定内容），再调用 /images/generations；需启用 Diffusion Translator）
          </span>
        </label>
      </template>
      <label class="flex cursor-pointer items-start gap-2">
        <input v-model="form.with_jailbreak" type="checkbox" class="mt-1 accent-accent" />
        <span>启用越狱提示词（在该 Agent 提示词外包裹破限内容，降低模型因审查拒绝创作黑暗/成人向剧情的概率）</span>
      </label>
    </div>

    <FormField label="System Prompt（留空使用默认）">
      <BaseTextarea v-model="form.system_prompt" rows="3" :placeholder="agentDefaultHint(form.role)" />
    </FormField>

    <div class="flex flex-wrap items-center gap-3 border-t border-line pt-4">
      <label class="flex cursor-pointer items-center gap-2 text-sm text-fg-muted">
        <input v-model="form.is_active" type="checkbox" class="accent-accent" /> 启用
      </label>
      <BaseButton variant="ghost" size="sm" class="ml-auto" :disabled="pinging()" @click="ping">
        {{ pinging() ? '测试中…' : '测试模型' }}
      </BaseButton>
      <BaseButton size="sm" :loading="saving" @click="save">保存配置</BaseButton>
    </div>
  </PaperCard>
</template>
