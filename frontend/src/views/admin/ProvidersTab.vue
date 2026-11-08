<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { ProviderConfig } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'
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
    <div class="flex items-center justify-between">
      <h3 class="font-type text-sm font-bold tracking-widest text-seal">LLM 提供商</h3>
      <BaseButton @click="openCreate">+ 新建提供商</BaseButton>
    </div>

    <ul class="space-y-2.5">
      <li v-for="p in providers" :key="p.id">
        <PaperCard class="flex items-start gap-4">
          <div class="min-w-0 flex-1">
            <div class="mb-1 flex flex-wrap items-center gap-2">
              <span class="font-semibold text-ink">{{ p.name }}</span>
              <StampBadge tone="ledger">{{ p.provider }}</StampBadge>
              <StampBadge v-if="!p.is_active" tone="seal">已停用</StampBadge>
            </div>
            <p class="truncate font-type text-xs text-ink-faint">{{ p.base_url || '（OpenAI 默认端点）' }}</p>
            <p class="mt-0.5 font-type text-xs" :class="p.api_key_set ? 'text-moss' : 'text-seal'">
              {{ p.api_key_set ? '✓ API Key 已配置' : '✗ 未配置 API Key' }}
            </p>
          </div>
          <div class="flex shrink-0 gap-1.5">
            <BaseButton variant="ghost" class="!px-3 !py-1 !text-xs" @click="openEdit(p)">编辑</BaseButton>
            <BaseButton variant="danger" class="!px-3 !py-1 !text-xs" @click="remove(p.id)">删除</BaseButton>
          </div>
        </PaperCard>
      </li>
      <li v-if="providers.length === 0" class="text-sm text-ink-faint">暂无提供商，请创建。</li>
    </ul>

    <ProviderModal :open="modalOpen" :editing="editing" @close="modalOpen = false" @saved="onSaved" />
  </div>
</template>
