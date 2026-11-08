<script setup lang="ts">
import { reactive, ref, useId, watch } from 'vue'
import { api } from '@/api/client'
import type { Scenario } from '@/api/scenarioTypes'
import type { CreateSessionForm, LobbySession } from '@/api/sessionTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import { scenarioContent } from '@/components/scenario/scenarioFormat'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'
import { difficultyLabel } from '@/utils/format'
import { applyScenarioSelection, buildCreatePayload, emptyCreateForm, errorMessage, scenarioOptionLabel } from './sessionLogic'
import { usePagedList } from './usePagedList'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: []; created: [session: LobbySession] }>()

const auth = useAuthStore()
const ui = useUiStore()
const pickerLabelId = useId()

const form = ref<CreateSessionForm>(emptyCreateForm())
// NOTE: 选中的剧本单独缓存：翻页后选中项不在当前页列表里，名称/简介仍要能显示和作为默认房间名
const selected = ref<Scenario | null>(null)
const submitting = ref(false)
const scenarios = reactive(usePagedList<Scenario>('/api/scenarios'))

async function loadScenarios(page?: number) {
  try {
    await scenarios.load(page)
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  }
}

// 每次打开都重置表单并回到第一页
watch(
  () => props.open,
  (open) => {
    if (!open) return
    selected.value = null
    form.value = emptyCreateForm()
    void loadScenarios(1)
  },
  { immediate: true },
)

function pick(s: Scenario) {
  selected.value = s
  form.value = applyScenarioSelection(form.value, s)
}

async function submit() {
  const payload = buildCreatePayload(form.value, selected.value)
  if (!payload) return
  submitting.value = true
  try {
    const created = await api<LobbySession>('POST', '/api/sessions', payload)
    ui.toast('房间创建成功！')
    emit('created', created)
    emit('close')
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <BaseModal :open="open" title="创建房间" max-width="max-w-lg" @close="emit('close')">
    <form class="space-y-4" @submit.prevent="submit">
      <BaseInput v-model="form.name" placeholder="房间名称（默认使用剧本名）" aria-label="房间名称" maxlength="200" />

      <div>
        <span :id="pickerLabelId" class="mb-1 block font-type text-xs tracking-wide text-ink-soft">选择剧本 *</span>
        <div
          role="group"
          :aria-labelledby="pickerLabelId"
          :aria-busy="scenarios.loading"
          class="max-h-56 space-y-1 overflow-y-auto rounded-sm border border-paper-edge bg-paper-deep/30 p-1.5"
        >
          <p v-if="!scenarios.loading && scenarios.items.length === 0" class="p-2 font-type text-xs text-ink-faint">暂无剧本</p>
          <button
            v-for="s in scenarios.items"
            :key="s.id"
            type="button"
            :aria-pressed="form.scenario_id === s.id"
            class="block w-full rounded-sm border p-2 text-left transition focus-visible:outline-2 focus-visible:outline-ledger"
            :class="form.scenario_id === s.id ? 'border-seal bg-seal/10' : 'border-transparent hover:bg-paper-deep'"
            @click="pick(s)"
          >
            <span class="block font-serif text-sm text-ink">{{ scenarioOptionLabel(s) }}</span>
            <span class="mt-0.5 block font-type text-[11px] text-ink-faint">{{ difficultyLabel(s.difficulty) }}</span>
          </button>
        </div>
        <div v-if="scenarios.totalPages > 1" class="mt-1.5 flex items-center justify-between font-type text-xs text-ink-faint">
          <button
            type="button"
            class="rounded-sm border border-paper-edge px-2 py-1 text-ink-soft transition hover:bg-paper-deep disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="scenarios.page <= 1"
            @click="scenarios.prev().catch((e) => ui.toast(errorMessage(e), 'error'))"
          >
            上一页
          </button>
          <span>第 {{ scenarios.page }} / {{ scenarios.totalPages }} 页 · 共 {{ scenarios.total }} 个</span>
          <button
            type="button"
            class="rounded-sm border border-paper-edge px-2 py-1 text-ink-soft transition hover:bg-paper-deep disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="scenarios.page >= scenarios.totalPages"
            @click="scenarios.next().catch((e) => ui.toast(errorMessage(e), 'error'))"
          >
            下一页
          </button>
        </div>
      </div>

      <div v-if="selected" class="space-y-2">
        <p class="font-type text-xs text-ink-soft">难度：<span>{{ difficultyLabel(selected.difficulty) }}</span></p>
        <p
          v-if="scenarioContent(selected).setting"
          class="whitespace-pre-wrap border-l-2 border-seal/50 pl-2 font-serif text-xs leading-relaxed text-ink-soft"
        >
          {{ scenarioContent(selected).setting }}
        </p>
        <p
          v-if="scenarioContent(selected).intro"
          class="whitespace-pre-wrap border-l-2 border-seal/50 pl-2 font-serif text-xs italic leading-relaxed text-ink"
        >
          {{ scenarioContent(selected).intro }}
        </p>
      </div>

      <div class="grid grid-cols-2 gap-3">
        <label class="block">
          <span class="mb-1 block font-type text-xs tracking-wide text-ink-soft">最大玩家数</span>
          <BaseInput v-model.number="form.max_players" type="number" min="1" max="8" />
        </label>
        <label class="block">
          <span class="mb-1 block font-type text-xs tracking-wide text-ink-soft">密码（可选）</span>
          <BaseInput v-model="form.password" type="text" placeholder="留空则公开" autocomplete="off" />
        </label>
      </div>

      <label v-if="auth.publicSettings.allowNSFW" class="flex items-center gap-2 font-type text-xs text-ink-soft">
        <input v-model="form.enable_nsfw" type="checkbox" class="accent-seal" />
        启用 NSFW 内容（创建后不可修改）
      </label>

      <div class="flex gap-2 pt-1">
        <BaseButton variant="ghost" class="flex-1" @click="emit('close')">取消</BaseButton>
        <BaseButton type="submit" class="flex-1" :loading="submitting" :disabled="!form.scenario_id">
          {{ submitting ? '创建中…' : '创建' }}
        </BaseButton>
      </div>
    </form>
  </BaseModal>
</template>
