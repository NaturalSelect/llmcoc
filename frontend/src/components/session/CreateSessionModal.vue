<script setup lang="ts">
import { Check } from '@lucide/vue'
import { computed, reactive, ref, useId, watch } from 'vue'
import { api } from '@/api/client'
import type { Scenario } from '@/api/scenarioTypes'
import type { CreateSessionForm, LobbySession } from '@/api/sessionTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import FormField from '@/components/base/FormField.vue'
import PaginationBar from '@/components/base/PaginationBar.vue'
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
const formId = useId()

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

// NOTE: PaginationBar 用 v-model:page 给出目标页码；这里直接按页码重新拉取，页码本身仍由分页列表维护
const scenarioPage = computed({
  get: () => scenarios.page,
  set: (target: number) => {
    void loadScenarios(target)
  },
})

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
    <form :id="formId" class="space-y-4" @submit.prevent="submit">
      <BaseInput v-model="form.name" placeholder="房间名称（默认使用剧本名）" aria-label="房间名称" maxlength="200" />

      <div>
        <span :id="pickerLabelId" class="mb-1.5 block text-sm text-fg-muted">选择剧本 *</span>
        <div
          role="group"
          :aria-labelledby="pickerLabelId"
          :aria-busy="scenarios.loading"
          class="space-y-1 rounded-lg bg-surface-2 p-1.5"
        >
          <p v-if="!scenarios.loading && scenarios.items.length === 0" class="p-2 text-sm text-fg-subtle">暂无剧本</p>
          <button
            v-for="s in scenarios.items"
            :key="s.id"
            type="button"
            :aria-pressed="form.scenario_id === s.id"
            class="flex w-full items-center justify-between gap-2 rounded-lg px-3 py-2 text-left transition focus-visible:outline-2 focus-visible:outline-accent"
            :class="form.scenario_id === s.id ? 'bg-accent/15' : 'hover:bg-surface-3'"
            @click="pick(s)"
          >
            <span class="min-w-0">
              <span class="block text-sm text-fg">{{ scenarioOptionLabel(s) }}</span>
              <span class="mt-0.5 block text-xs text-fg-subtle">{{ difficultyLabel(s.difficulty) }}</span>
            </span>
            <Check v-if="form.scenario_id === s.id" :size="16" :stroke-width="1.75" class="shrink-0 text-accent" />
          </button>
        </div>
        <PaginationBar v-model:page="scenarioPage" :total-pages="scenarios.totalPages" :total="scenarios.total" />
      </div>

      <div v-if="selected" class="space-y-2 rounded-lg bg-surface-2 p-3">
        <p class="text-sm text-fg-muted">难度：<span>{{ difficultyLabel(selected.difficulty) }}</span></p>
        <p v-if="scenarioContent(selected).setting" class="whitespace-pre-wrap font-prose text-prose text-fg-muted">
          {{ scenarioContent(selected).setting }}
        </p>
      </div>

      <div class="grid grid-cols-2 gap-3">
        <FormField label="最大玩家数">
          <BaseInput v-model.number="form.max_players" type="number" min="1" max="8" class="tabular-nums" />
        </FormField>
        <FormField label="密码（可选）">
          <BaseInput v-model="form.password" type="text" placeholder="留空则公开" autocomplete="off" />
        </FormField>
      </div>

      <label v-if="auth.publicSettings.allowNSFW" class="flex items-center gap-2 text-sm text-fg-muted">
        <input v-model="form.enable_nsfw" type="checkbox" class="accent-accent" />
        启用 NSFW 内容（创建后不可修改）
      </label>

      <label class="flex items-start gap-2 text-sm text-fg-muted">
        <input v-model="form.evolve_backstory" type="checkbox" class="mt-0.5 accent-accent" />
        <span>
          通关后改写存活调查员的个人经历（创建后不可修改）
          <span class="block text-xs text-fg-subtle">改写不可撤销，默认关闭；失败或阵亡的调查员不受影响。</span>
        </span>
      </label>
    </form>

    <template #footer>
      <p class="mb-2.5 truncate text-sm" :class="selected ? 'text-fg' : 'text-fg-subtle'">
        {{ selected ? '已选剧本：' + selected.name : '请先选择一个剧本' }}
      </p>
      <div class="flex gap-2">
        <BaseButton variant="ghost" class="flex-1" @click="emit('close')">取消</BaseButton>
        <BaseButton type="submit" :form="formId" class="flex-1" :loading="submitting" :disabled="!form.scenario_id">
          {{ submitting ? '创建中…' : '创建' }}
        </BaseButton>
      </div>
    </template>
  </BaseModal>
</template>
