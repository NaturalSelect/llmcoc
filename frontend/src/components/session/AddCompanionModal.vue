<script setup lang="ts">
import { Info, UserRound } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { ApiError } from '@/api/client'
import { companionApi } from '@/api/companionApi'
import type { CompanionCard } from '@/api/companionTypes'
import type { Scenario } from '@/api/scenarioTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import FormField from '@/components/base/FormField.vue'
import Skeleton from '@/components/base/Skeleton.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import StatBar from '@/components/base/StatBar.vue'
import { portraitSrc } from '@/components/character/characterLogic'
import { scenarioContent } from '@/components/scenario/scenarioFormat'
import { buildCompanionPayload, companionCardMeta } from '@/game/companion'
import { useUiStore } from '@/stores/ui'
import { claimedHandoutCodes, errorMessage, isHandoutClaimed } from './sessionLogic'

// NOTE: 房主在大厅阶段给房间添加 AI 陪玩：从 AI 卡池的空闲卡里选一张，可选预设 HO。结构参照 JoinSessionModal（同样没有秘密背景输入），
// 打开时才请求卡池（每次打开都重拉，卡可能刚被别的房间占走）；enabled=false 表示管理员没有启用 companion 角色，此时只提示、不能提交
const props = defineProps<{
  open: boolean
  session: {
    id: number
    name?: string
    scenario?: Scenario | null
    players?: ReadonlyArray<{ handout_code?: unknown }> | null
  } | null
}>()
const emit = defineEmits<{ close: []; changed: [] }>()

const ui = useUiStore()

const loading = ref(false)
const loaded = ref(false)
const loadError = ref('')
const enabled = ref(false)
const cards = ref<CompanionCard[]>([])
const selectedId = ref<number | null>(null)
const handoutCode = ref('')
const submitting = ref(false)
// NOTE: 每次请求 +1，关闭弹窗或重新打开后迟到的旧响应据此丢弃
let loadGen = 0

const handouts = computed(() => (scenarioContent(props.session?.scenario).handouts ?? []).filter((h) => Boolean(h?.code?.trim())))
const claimedCodes = computed(() => claimedHandoutCodes(props.session?.players))
const hasUnclaimedHandout = computed(() => handouts.value.some((h) => !isHandoutClaimed(claimedCodes.value, h.code)))
const notEnabled = computed(() => loaded.value && !loadError.value && !enabled.value)
const canSubmit = computed(() => loaded.value && enabled.value && !loadError.value && selectedId.value !== null && !submitting.value)

function resetForm() {
  loadGen++
  loading.value = false
  loaded.value = false
  loadError.value = ''
  enabled.value = false
  cards.value = []
  selectedId.value = null
  handoutCode.value = ''
}

async function load() {
  const sid = props.session?.id
  if (!sid) return
  const gen = ++loadGen
  loading.value = true
  loadError.value = ''
  try {
    const res = await companionApi.cards(sid)
    if (gen !== loadGen) return
    enabled.value = res.enabled
    cards.value = res.cards
    // 只有一张空闲卡时直接选中，与加入房间的选卡行为一致
    selectedId.value = res.cards.length === 1 ? res.cards[0].id : null
    loaded.value = true
  } catch (e) {
    if (gen !== loadGen) return
    loadError.value = errorMessage(e)
  } finally {
    if (gen === loadGen) loading.value = false
  }
}

watch(
  () => props.open,
  (open) => {
    resetForm()
    if (open) void load()
  },
  { immediate: true },
)

function cardClass(id: number): string {
  return selectedId.value === id ? 'border-accent bg-accent/10' : 'border-line bg-surface-2/60 hover:border-accent/50'
}

async function submit() {
  const sid = props.session?.id
  const payload = buildCompanionPayload({
    character_card_id: selectedId.value ?? '',
    handout_code: handoutCode.value,
  })
  if (!sid || !payload || !canSubmit.value) return
  submitting.value = true
  try {
    const res = await companionApi.add(sid, payload)
    emit('close')
    ui.toast(res?.handout_code ? `AI 陪玩已加入，获得身份卡 ${res.handout_code}` : 'AI 陪玩已加入')
    emit('changed')
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
    // NOTE: 409 表示房间里已经有 AI（别人刚加过），本地的房间信息过期了：关掉弹窗并刷新，让按钮状态跟上
    if (e instanceof ApiError && e.status === 409) {
      emit('close')
      emit('changed')
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <BaseModal :open="open" title="添加 AI 陪玩" max-width="max-w-md" @close="emit('close')">
    <p class="mb-1 text-sm font-medium text-fg">{{ session?.name }}</p>
    <p class="mb-4 text-xs text-fg-subtle">AI 占一个玩家名额，只在被 KP 点名时回应；每个房间至多一个 AI。</p>

    <form class="space-y-3" @submit.prevent="submit">
      <div v-if="loading" class="space-y-2" aria-busy="true" aria-label="正在加载 AI 卡池">
        <Skeleton class="h-20" />
        <Skeleton class="h-20" />
      </div>

      <div v-else-if="loadError" class="space-y-2 rounded-lg border border-danger/40 bg-danger/10 p-3" role="alert">
        <p class="text-sm text-danger">{{ loadError }}</p>
        <BaseButton variant="ghost" size="sm" @click="load">重试</BaseButton>
      </div>

      <div v-else-if="notEnabled" class="flex items-start gap-2 rounded-lg border border-line bg-surface-2/70 p-3 text-sm text-fg-muted" role="status">
        <Info :size="16" :stroke-width="1.75" class="mt-0.5 shrink-0 text-fg-subtle" />
        管理员尚未启用 AI 陪玩
      </div>

      <EmptyState v-else-if="loaded && cards.length === 0">AI 卡池里暂时没有空闲的人物卡，请联系管理员添加</EmptyState>

      <template v-else-if="loaded">
        <div role="radiogroup" aria-label="选择 AI 使用的人物卡" class="max-h-72 space-y-1.5 overflow-y-auto pr-0.5">
          <button
            v-for="c in cards"
            :key="c.id"
            type="button"
            role="radio"
            :aria-checked="selectedId === c.id"
            class="flex w-full items-center gap-3 rounded-lg border p-2.5 text-left transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            :class="cardClass(c.id)"
            @click="selectedId = c.id"
          >
            <img v-if="portraitSrc(c)" :src="portraitSrc(c)" alt="" class="h-16 w-11 shrink-0 rounded-lg bg-surface-2 object-cover object-top" />
            <span v-else class="flex h-16 w-11 shrink-0 items-center justify-center rounded-lg bg-surface-2 text-fg-subtle">
              <UserRound :size="20" :stroke-width="1.5" />
            </span>
            <span class="min-w-0 flex-1 space-y-1.5">
              <span class="flex items-center justify-between gap-2">
                <span class="min-w-0 truncate text-sm font-semibold text-fg">{{ c.name }}</span>
                <StampBadge v-if="selectedId === c.id" class="shrink-0" tone="moss">已选</StampBadge>
              </span>
              <span class="block truncate text-xs text-fg-subtle">{{ companionCardMeta(c) }}</span>
              <StatBar label="HP" tone="hp" :value="c.hp" :max="c.max_hp" />
              <StatBar label="SAN" tone="san" :value="c.san" :max="c.max_san" />
            </span>
          </button>
        </div>

        <FormField v-if="handouts.length" label="模组预设身份卡 (HO，可选)">
          <BaseSelect v-model="handoutCode">
            <option value="">不分配</option>
            <option value="random" :disabled="!hasUnclaimedHandout">随机抽签</option>
            <option v-for="h in handouts" :key="h.code" :value="h.code" :disabled="isHandoutClaimed(claimedCodes, h.code)">
              {{ h.code }}{{ h.title ? ' ' + h.title : '' }}{{ isHandoutClaimed(claimedCodes, h.code) ? '（已认领）' : '' }}
            </option>
          </BaseSelect>
        </FormField>
      </template>

      <div class="flex gap-2 pt-1">
        <BaseButton variant="ghost" class="flex-1" @click="emit('close')">取消</BaseButton>
        <BaseButton
          type="submit"
          class="flex-1"
          :loading="submitting"
          :disabled="!canSubmit"
          :title="notEnabled ? '管理员尚未启用 AI 陪玩' : undefined"
        >
          {{ submitting ? '添加中…' : '添加' }}
        </BaseButton>
      </div>
    </form>
  </BaseModal>
</template>
