<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ArrowRight, Dices } from '@lucide/vue'
import type { RollDraft, SkillBudget } from '@/api/characterTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import BaseTextarea from '@/components/base/BaseTextarea.vue'
import FormField from '@/components/base/FormField.vue'
import { useUiStore } from '@/stores/ui'
import { characterApi } from './characterApi'
import { errMessage } from './characterLogic'
import { registerCreatedCard } from './createdCard'
import DraftSummary from './DraftSummary.vue'
import {
  DODGE_SKILL,
  MAX_AGE,
  MAX_SKILL,
  MIN_AGE,
  NATIVE_SKILL,
  applyRoll,
  buildFinalizePayload,
  clampSkill,
  emptyManualForm,
  lockSkills,
  remainingPoints,
  validateFinalize,
  validateRollAge,
  visibleSkillNames,
  type ManualForm,
  type SkillDefaults,
  type SkillValues,
} from './manualCreate'

const props = defineProps<{ active: boolean }>()
const emit = defineEmits<{ close: [] }>()

const ui = useUiStore()

const form = reactive<ManualForm>(emptyManualForm())
const step = ref(1)
const draft = ref<RollDraft | null>(null)
const defaults = ref<SkillDefaults>({})
const skills = ref<SkillValues>({})
const budget = ref<SkillBudget>({ occupation: 0, interest: 0, total: 0 })
const names = ref<string[]>([])
const loading = ref(false)

const remaining = computed(() => remainingPoints(budget.value, defaults.value, skills.value))
const canContinue = computed(() => !!draft.value && !!form.name.trim() && !!form.gender)

const STEPS = ['1 年龄投掷', '2 基本资料', '3 技能分配']

async function loadSkillDefaults(): Promise<void> {
  const r = await characterApi.skillDefaults()
  defaults.value = lockSkills(r.skill_defaults || {}, draft.value?.stats)
  names.value = visibleSkillNames(r.skill_names, defaults.value)
}

// NOTE: 技能清单只是展示用，拿不到时静默；真正的校验以服务器为准
onMounted(() => loadSkillDefaults().catch(() => {}))

watch(
  () => props.active,
  (active) => {
    if (!active) return
    step.value = draft.value ? 2 : 1
    loadSkillDefaults().catch(() => {})
  },
)

async function roll(): Promise<void> {
  const bad = validateRollAge(form.age)
  if (bad) {
    ui.toast(bad, 'error')
    return
  }
  loading.value = true
  try {
    const r = await characterApi.roll(Number(form.age || 0))
    const out = applyRoll(r, { defaults: defaults.value, names: names.value })
    draft.value = r
    defaults.value = out.defaults
    budget.value = out.budget
    names.value = out.names
    skills.value = out.skills
    step.value = 2
    ui.toast('属性已锁定，请继续填写调查员资料')
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    loading.value = false
  }
}

function clamp(name: string): void {
  skills.value[name] = clampSkill(name, skills.value[name], defaults.value, draft.value?.stats)
}

function commit(name: string, e: Event): void {
  clamp(name)
  ;(e.target as HTMLInputElement).blur()
}

async function finalize(): Promise<void> {
  const d = draft.value
  const err = validateFinalize(d, form, remaining.value)
  if (err || !d) {
    ui.toast(err ?? '请先投掷并锁定属性', 'error')
    return
  }
  loading.value = true
  try {
    const card = await characterApi.finalize(buildFinalizePayload(d, form, skills.value, defaults.value))
    emit('close')
    await registerCreatedCard(card, '规则车卡创建成功，AI背景已生成！')
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="space-y-5" role="tabpanel">
    <ol class="flex flex-wrap items-center gap-2 text-sm">
      <template v-for="(label, i) in STEPS" :key="label">
        <li
          class="rounded-full px-3 py-1 tabular-nums"
          :class="step === i + 1 ? 'bg-accent/15 font-medium text-accent' : step > i + 1 ? 'bg-surface-3 text-fg-muted' : 'text-fg-subtle'"
          :aria-current="step === i + 1 ? 'step' : undefined"
        >
          {{ label }}
        </li>
        <li v-if="i < STEPS.length - 1" class="text-fg-subtle" aria-hidden="true">
          <ArrowRight :size="16" :stroke-width="1.75" />
        </li>
      </template>
    </ol>

    <div v-show="step === 1" class="space-y-4 rounded-xl bg-surface-2 p-4">
      <p class="text-sm text-fg-muted">规则车卡会先在服务器掷骰并锁定属性，草稿15分钟内有效且只能使用一次。</p>
      <div class="grid grid-cols-3 items-end gap-3">
        <FormField class="col-span-2" :label="`年龄（${MIN_AGE}-${MAX_AGE}）`">
          <BaseInput v-model.number="form.age" type="number" :min="MIN_AGE" :max="MAX_AGE" />
        </FormField>
        <BaseButton :loading="loading" @click="roll">
          <Dices v-if="!loading" :size="16" :stroke-width="1.75" />
          {{ loading ? '掷骰中…' : '服务器掷骰' }}
        </BaseButton>
      </div>
    </div>

    <DraftSummary v-if="draft" :draft="draft" />

    <div v-show="step === 2" class="space-y-3 rounded-xl bg-surface-2 p-4">
      <div class="grid grid-cols-2 gap-3">
        <BaseInput v-model="form.name" placeholder="姓名 *" aria-label="姓名" />
        <BaseSelect v-model="form.gender" aria-label="性别">
          <option value="">性别 *</option>
          <option value="男">男</option>
          <option value="女">女</option>
        </BaseSelect>
      </div>
      <div class="grid grid-cols-3 gap-3">
        <BaseInput v-model="form.occupation" placeholder="职业" aria-label="职业" />
        <BaseInput v-model="form.birthplace" placeholder="出生地" aria-label="出生地" />
        <BaseInput v-model="form.residence" placeholder="现居地" aria-label="现居地" />
      </div>
      <BaseTextarea v-model="form.creation_hint" rows="4" placeholder="角色构思（可选）：如家族背景、性格倾向、希望的氛围等" aria-label="角色构思" />
      <p class="text-xs text-fg-subtle">AI会根据提示词、姓名、性别、职业和已锁定属性生成背景故事、外貌与性格特征。</p>
      <div class="flex gap-2 pt-1">
        <BaseButton variant="ghost" class="flex-1" @click="step = 1">重新投掷</BaseButton>
        <BaseButton class="flex-1" :disabled="!canContinue" @click="step = 3">下一步：分配技能</BaseButton>
      </div>
    </div>

    <div v-show="step === 3" class="space-y-3 rounded-xl bg-surface-2 p-4">
      <div class="flex items-center justify-between text-sm">
        <span class="text-fg-muted">技能点：职业 {{ budget.occupation || 0 }} + 兴趣 {{ budget.interest || 0 }}</span>
        <span class="font-semibold tabular-nums" :class="remaining < 0 ? 'text-danger' : 'text-fg'" role="status">剩余 {{ remaining }}</span>
      </div>
      <p class="text-xs text-fg-subtle">技能最终值不能低于基础值且最高90；母语和闪避由服务器强制计算，克苏鲁神话不可分配。</p>
      <div class="grid max-h-72 grid-cols-1 gap-2 overflow-y-auto pr-1 md:grid-cols-2">
        <div v-for="(name, i) in names" :key="name" class="flex items-center gap-2 text-sm">
          <label :for="`skill-input-${i}`" class="w-28 shrink-0 truncate text-fg-muted">{{ name }}</label>
          <span class="w-16 shrink-0 text-xs tabular-nums text-fg-subtle">基础 {{ defaults[name] ?? 0 }}</span>
          <BaseInput
            :id="`skill-input-${i}`"
            v-model.number="skills[name]"
            type="number"
            :min="defaults[name] ?? 0"
            :max="MAX_SKILL"
            :disabled="name === NATIVE_SKILL || name === DODGE_SKILL"
            class="flex-1 text-center tabular-nums"
            @blur="clamp(name)"
            @keydown.enter.prevent="commit(name, $event)"
          />
        </div>
      </div>
      <p class="text-xs text-fg-subtle">目前前端提供通用技能清单；职业点与兴趣点合并为总预算，最终以服务器校验为准。</p>
      <div class="flex gap-2 pt-1">
        <BaseButton variant="ghost" class="flex-1" @click="step = 2">返回资料</BaseButton>
        <BaseButton class="flex-1" :loading="loading" :disabled="remaining < 0" @click="finalize">
          {{ loading ? 'AI生成中…' : '确认创建并生成背景' }}
        </BaseButton>
      </div>
    </div>
  </div>
</template>
