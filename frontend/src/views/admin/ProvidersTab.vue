<script setup lang="ts">
import { CircleCheck, CircleX, Pencil, Trash2 } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import type { ProviderConfig } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import ProviderModal from '@/components/admin/providers/ProviderModal.vue'
import { errMsg } from '@/components/admin/shared/errMsg'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()

const providers = ref<ProviderConfig[]>([])
const modalOpen = ref(false)
const editing = ref<ProviderConfig | null>(null)

async function loadProviders() {
  providers.value = (await api<ProviderConfig[] | null>('GET', '/api/admin/config/providers')) || []
}

async function safeLoad() {
  try {
    await loadProviders()
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

function openCreate() {
  editing.value = null
  modalOpen.value = true
}

function openEdit(p: ProviderConfig) {
  editing.value = p
  modalOpen.value = true
}

async function onSaved() {
  modalOpen.value = false
  await safeLoad()
}

async function remove(id: number) {
  if (!(await ui.confirm('确认删除此提供商？', { danger: true, confirmText: '删除' }))) return
  try {
    await api('DELETE', '/api/admin/config/providers/' + id)
    ui.toast('已删除')
    await loadProviders()
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

onMounted(safeLoad)
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between gap-3">
      <SectionHeading>LLM 提供商</SectionHeading>
      <BaseButton @click="openCreate">+ 新建提供商</BaseButton>
    </div>

    <ul class="space-y-3">
      <li v-for="p in providers" :key="p.id">
        <PaperCard class="flex flex-wrap items-start gap-4">
          <div class="min-w-0 flex-1">
            <div class="mb-1.5 flex flex-wrap items-center gap-2">
              <span class="text-base font-semibold text-fg">{{ p.name }}</span>
              <StampBadge tone="ledger">{{ p.provider }}</StampBadge>
              <StampBadge v-if="!p.is_active" tone="danger">已停用</StampBadge>
            </div>
            <p class="truncate font-mono text-xs text-fg-subtle">{{ p.base_url || '（OpenAI 默认端点）' }}</p>
            <p class="mt-1 flex items-center gap-1.5 text-sm" :class="p.api_key_set ? 'text-success' : 'text-danger'">
              <CircleCheck v-if="p.api_key_set" :size="16" :stroke-width="1.75" />
              <CircleX v-else :size="16" :stroke-width="1.75" />
              {{ p.api_key_set ? 'API Key 已配置' : '未配置 API Key' }}
            </p>
          </div>
          <div class="flex shrink-0 gap-2">
            <BaseButton variant="ghost" size="sm" @click="openEdit(p)"><Pencil :size="16" :stroke-width="1.75" />编辑</BaseButton>
            <BaseButton variant="danger" size="sm" @click="remove(p.id)"><Trash2 :size="16" :stroke-width="1.75" />删除</BaseButton>
          </div>
        </PaperCard>
      </li>
    </ul>
    <EmptyState v-if="providers.length === 0">暂无提供商，请创建。</EmptyState>

    <ProviderModal :open="modalOpen" :editing="editing" @close="modalOpen = false" @saved="onSaved" />
  </div>
</template>
