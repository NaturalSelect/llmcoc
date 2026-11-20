<script setup lang="ts">
import { Shuffle, Sparkles } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { Scenario } from '@/api/scenarioTypes'
import type { JoinSessionForm, ScenarioHandout } from '@/api/sessionTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { playersLabel, scenarioContent, splitTags } from '@/components/scenario/scenarioFormat'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'
import { difficultyLabel } from '@/utils/format'
import {
  buildJoinPayload,
  cardOptionLabel,
  claimedHandoutCodes,
  emptyJoinForm,
  errorMessage,
  isHandoutClaimed,
  joinableCharacters,
} from './sessionLogic'

// NOTE: 大厅卡片（LobbySession）和游戏页里的房间（Session）都要用，scenario/players 可选，供加入前展示无剧透推荐技能与预设 HO
const props = defineProps<{
  open: boolean
  session: {
    id: number
    name?: string
    has_password?: boolean
    scenario?: Scenario | null
    players?: ReadonlyArray<{ handout_code?: unknown }> | null
  } | null
}>()
const emit = defineEmits<{ close: []; joined: [] }>()

const chars = useCharactersStore()
const ui = useUiStore()

const form = ref<JoinSessionForm>(emptyJoinForm())
const handoutCode = ref('')
const joining = ref(false)
const candidates = computed(() => joinableCharacters(chars.characters))
const scenario = computed(() => props.session?.scenario ?? null)
const scenarioTags = computed(() => splitTags(scenario.value?.tags))
const recommendedSkills = computed(() =>
  Array.isArray(scenario.value?.recommended_skills) ? scenario.value.recommended_skills.filter((s): s is string => typeof s === 'string' && s.length > 0) : [],
)
const handouts = computed<ScenarioHandout[]>(() =>
  (scenarioContent(scenario.value).handouts ?? []).filter((h) => Boolean(h?.code?.trim())),
)
const claimedCodes = computed(() => claimedHandoutCodes(props.session?.players))
const hasUnclaimedHandout = computed(() => handouts.value.some((h) => !isHandoutClaimed(claimedCodes.value, h.code)))

function handoutCardClass(code: string | undefined): string {
  const claimed = isHandoutClaimed(claimedCodes.value, code)
  if (claimed) return 'cursor-not-allowed border-line bg-surface-2/40 opacity-50'
  if (handoutCode.value === (code ?? '')) return 'border-accent bg-accent/10'
  return 'border-line bg-surface-2/60 hover:border-accent/50'
}

// 每次打开都清空上次选择的人物卡、密码和 HO；只有一张可用卡时直接选中
watch(
  () => props.open,
  (open) => {
    if (!open) return
    form.value = emptyJoinForm()
    handoutCode.value = ''
    const only = candidates.value.length === 1 ? candidates.value[0] : undefined
    if (only?.id) form.value.character_card_id = only.id
  },
  { immediate: true },
)

async function submit() {
  const payload = buildJoinPayload({
    ...form.value,
    handout_code: handoutCode.value,
  })
  if (!props.session || !payload) return
  joining.value = true
  try {
    const res = await api<{ handout_code?: string }>('POST', `/api/sessions/${props.session.id}/join`, payload)
    emit('close')
    ui.toast(res?.handout_code ? `加入成功！已获得身份卡 ${res.handout_code}` : '加入成功！')
    emit('joined')
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  } finally {
    joining.value = false
  }
}
</script>

<template>
  <BaseModal :open="open" title="加入房间" max-width="max-w-md" @close="emit('close')">
    <p class="mb-3 text-sm font-medium text-fg">{{ session?.name }}</p>

    <div v-if="scenario && (scenario.name || recommendedSkills.length || scenarioTags.length)" class="mb-4 space-y-2 rounded-lg border border-line bg-surface-2/70 p-3">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-fg-muted">
        <span v-if="scenario.name" class="font-medium text-accent">{{ scenario.name }}</span>
        <span v-if="scenario.difficulty">难度：{{ difficultyLabel(scenario.difficulty) }}</span>
        <span v-if="scenario.min_players || scenario.max_players">{{ playersLabel(scenario.min_players, scenario.max_players) }}</span>
      </div>
      <div v-if="scenarioTags.length" class="flex flex-wrap gap-1">
        <span v-for="t in scenarioTags" :key="t" class="rounded-full bg-surface-3 px-2 py-0.5 text-xs text-fg-subtle">{{ t }}</span>
      </div>
      <div v-if="recommendedSkills.length" class="flex flex-wrap items-center gap-1.5">
        <span class="text-xs text-fg-subtle">推荐技能：</span>
        <span v-for="sk in recommendedSkills" :key="sk" class="rounded-full bg-accent/15 px-2 py-0.5 text-xs text-accent">{{ sk }}</span>
      </div>
    </div>

    <form class="space-y-3" @submit.prevent="submit">
      <BaseSelect v-model="form.character_card_id" aria-label="选择携带的人物卡">
        <option value="">选择携带的人物卡 *</option>
        <option v-for="c in candidates" :key="c.id" :value="c.id">{{ cardOptionLabel(c) }}</option>
      </BaseSelect>
      <BaseInput v-if="session?.has_password" v-model="form.password" type="text" placeholder="房间密码" aria-label="房间密码" autocomplete="off" />

      <div v-if="handouts.length" class="space-y-2 rounded-lg border border-line bg-surface-2/40 p-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="inline-flex items-center gap-1.5 text-xs font-medium text-fg">
            <Sparkles :size="14" :stroke-width="1.75" class="shrink-0 text-accent" />
            模组预设身份卡 (HO)
          </span>
          <div class="flex items-center gap-1.5">
            <button
              type="button"
              :disabled="hasUnclaimedHandout === false"
              class="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs transition disabled:cursor-not-allowed disabled:opacity-45"
              :class="handoutCode === 'random' ? 'border-accent bg-accent/15 text-accent' : 'border-line bg-surface text-fg-muted hover:text-fg'"
              @click="handoutCode = handoutCode === 'random' ? '' : 'random'"
            >
              <Shuffle :size="12" :stroke-width="1.75" />
              随机抽签
            </button>
            <button
              v-if="handoutCode"
              type="button"
              class="text-xs text-fg-subtle hover:text-fg"
              @click="handoutCode = ''"
            >
              不选
            </button>
          </div>
        </div>
        <p class="text-xs text-fg-subtle">认领后秘密正文将仅对你与 KP 可见。</p>
        <div class="max-h-44 space-y-1.5 overflow-y-auto pr-0.5">
          <button
            v-for="h in handouts"
            :key="h.code"
            type="button"
            :disabled="isHandoutClaimed(claimedCodes, h.code)"
            class="w-full rounded-lg border px-3 py-2 text-left transition"
            :class="handoutCardClass(h.code)"
            @click="handoutCode = handoutCode === h.code ? '' : (h.code || '')"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="text-xs font-semibold text-fg">
                <span class="text-accent">{{ h.code }}</span>
                <span v-if="h.title" class="ml-1.5">{{ h.title }}</span>
              </span>
              <StampBadge v-if="isHandoutClaimed(claimedCodes, h.code)" tone="faint">已认领</StampBadge>
              <StampBadge v-else-if="handoutCode === h.code" tone="moss">已选</StampBadge>
            </div>
            <p v-if="h.public_hook" class="mt-1 text-xs leading-relaxed text-fg-muted">{{ h.public_hook }}</p>
          </button>
        </div>
      </div>

      <div class="flex gap-2 pt-1">
        <BaseButton variant="ghost" class="flex-1" @click="emit('close')">取消</BaseButton>
        <BaseButton type="submit" class="flex-1" :loading="joining" :disabled="!form.character_card_id">
          {{ joining ? '加入中…' : '加入' }}
        </BaseButton>
      </div>
    </form>
  </BaseModal>
</template>
