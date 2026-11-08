<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { ProviderConfig, ProviderForm } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import { useUiStore } from '@/stores/ui'
import { errMsg } from '../shared/errMsg'

// NOTE: 只从「LLM 提供商」tab 打开；editing 为空表示新建
const props = defineProps<{ open: boolean; editing: ProviderConfig | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const ui = useUiStore()

const KNOWN_PROVIDERS = ['openai', 'anthropic']

function blankForm(): ProviderForm {
  return { name: '', provider: 'openai', base_url: '', api_key: '', is_active: true }
}

const form = reactive<ProviderForm>(blankForm())
const saving = ref(false)

// NOTE: 每次打开都重置表单；编辑时 API Key 留空表示保留原值（后端只在非空时才更新）
watch(
  () => props.open,
  (open) => {
    if (!open) return
    const p = props.editing
    Object.assign(
      form,
      p ? { name: p.name, provider: p.provider, base_url: p.base_url, api_key: '', is_active: p.is_active } : blankForm(),
    )
  },
  { immediate: true },
)

// NOTE: 后端还允许 custom；遇到不在下拉里的值时补一个选项，避免下拉框显示成空白
const hasUnknownProvider = computed(() => !!form.provider && !KNOWN_PROVIDERS.includes(form.provider))

async function save() {
  saving.value = true
  try {
    if (props.editing) {
      await api('PUT', '/api/admin/config/providers/' + props.editing.id, { ...form })
      ui.toast('提供商已更新')
    } else {
      await api('POST', '/api/admin/config/providers', { ...form })
      ui.toast('提供商已创建')
    }
    emit('saved')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <BaseModal :open="open" :title="editing ? '编辑提供商' : '新建 LLM 提供商'" max-width="max-w-md" @close="emit('close')">
    <form class="space-y-3" @submit.prevent="save">
      <BaseInput v-model="form.name" placeholder="显示名称 *" aria-label="显示名称" />
      <BaseSelect v-model="form.provider" aria-label="提供商类型">
        <option value="openai">openai（兼容 OpenAI API）</option>
        <option value="anthropic">anthropic（Claude 官方 API）</option>
        <option v-if="hasUnknownProvider" :value="form.provider">{{ form.provider }}</option>
      </BaseSelect>
      <BaseInput v-model="form.base_url" placeholder="Base URL（留空使用官方端点）" aria-label="Base URL" />
      <BaseInput
        v-model="form.api_key"
        type="password"
        autocomplete="new-password"
        :placeholder="editing ? '新 API Key（留空保留原值）' : 'API Key'"
        aria-label="API Key"
      />
      <label class="flex cursor-pointer items-center gap-2 text-sm text-fg-muted">
        <input v-model="form.is_active" type="checkbox" class="accent-accent" /> 启用
      </label>
      <div class="flex gap-2 pt-1">
        <BaseButton variant="ghost" class="flex-1" @click="emit('close')">取消</BaseButton>
        <BaseButton type="submit" class="flex-1" :loading="saving">保存</BaseButton>
      </div>
    </form>
  </BaseModal>
</template>
