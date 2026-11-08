import type {
  CharacterStatsFull,
  FinalizePayload,
  GeneratePayload,
  RollDraft,
  SkillBudget,
} from '@/api/characterTypes'

export const NATIVE_SKILL = '母语'
export const DODGE_SKILL = '闪避'
export const MYTHOS_SKILL = '克苏鲁神话'
export const MIN_AGE = 15
export const MAX_AGE = 90
export const MAX_SKILL = 90

// NOTE: 技能输入框是 number 输入，用户清空时值为空字符串，所以允许 string
export type SkillValues = Record<string, number | string>
export type SkillDefaults = Record<string, number>

export interface ManualForm {
  name: string
  gender: string
  age: number | string | null | undefined
  occupation: string
  birthplace: string
  residence: string
  creation_hint: string
}

export function emptyManualForm(): ManualForm {
  return { name: '', gender: '', age: 25, occupation: '', birthplace: '', residence: '', creation_hint: '' }
}

export interface AiForm {
  name: string
  race: string
  gender: string
  age: number | string | null | undefined
  occupation: string
  era: string
  background: string
}

export function emptyAiForm(): AiForm {
  return { name: '', race: '人类', gender: '', age: '', occupation: '', era: '', background: '' }
}

// NOTE: 年龄留空时不发送：旧前端会发 ""，后端 Age 是 int，反序列化直接失败；缺省则由后端钳制到 15
export function aiPayload(form: AiForm): GeneratePayload {
  const { age, ...rest } = form
  return typeof age === 'number' && Number.isFinite(age) ? { ...rest, age } : rest
}

export function validateRollAge(age: unknown): string | null {
  const n = Number(age || 0)
  return Number.isFinite(n) && n >= MIN_AGE && n <= MAX_AGE ? null : `年龄必须在${MIN_AGE}-${MAX_AGE}之间`
}

// NOTE: 母语 = EDU、闪避 = DEX/2 由服务器强制计算，前端只做展示用的同步
export function lockSkills(defaults: SkillDefaults, stats: CharacterStatsFull | undefined): SkillDefaults {
  const out = { ...defaults }
  if (stats) {
    out[NATIVE_SKILL] = stats.edu || out[NATIVE_SKILL] || 0
    out[DODGE_SKILL] = Math.floor((stats.dex || 0) / 2)
  }
  return out
}

export function visibleSkillNames(names: string[] | null | undefined, defaults: SkillDefaults): string[] {
  return (names || Object.keys(defaults)).filter((n) => n !== MYTHOS_SKILL)
}

export interface RollOutcome {
  defaults: SkillDefaults
  budget: SkillBudget
  names: string[]
  skills: SkillValues
}

export function applyRoll(draft: RollDraft, prev: { defaults: SkillDefaults; names: string[] }): RollOutcome {
  const defaults = lockSkills(draft.skill_defaults || prev.defaults || {}, draft.stats)
  const names = (prev.names.length ? prev.names : Object.keys(defaults).sort()).filter((n) => n !== MYTHOS_SKILL)
  return {
    defaults,
    budget: draft.skill_budget || { occupation: 0, interest: 0, total: 0 },
    names,
    skills: { ...defaults },
  }
}

// NOTE: 只统计高于基础值的部分；母语、闪避、克苏鲁神话不占预算
export function spentPoints(defaults: SkillDefaults, skills: SkillValues): number {
  return Object.keys(skills).reduce((sum, name) => {
    if (name === NATIVE_SKILL || name === DODGE_SKILL || name === MYTHOS_SKILL) return sum
    const base = Number(defaults[name] ?? 0)
    const val = Number(skills[name] ?? base)
    return sum + Math.max(0, val - base)
  }, 0)
}

export function remainingPoints(budget: SkillBudget | null | undefined, defaults: SkillDefaults, skills: SkillValues): number {
  return Number(budget?.total || 0) - spentPoints(defaults, skills)
}

export function clampSkill(
  name: string,
  value: number | string | undefined,
  defaults: SkillDefaults,
  stats: CharacterStatsFull | undefined,
): number {
  const base = Number(defaults[name] ?? 0)
  if (name === NATIVE_SKILL) return stats?.edu || base
  if (name === DODGE_SKILL) return Math.floor((stats?.dex || 0) / 2)
  let val = Number(value ?? base)
  if (!Number.isFinite(val)) val = base
  if (val < base) val = base
  if (val > MAX_SKILL) val = MAX_SKILL
  return Math.floor(val)
}

// NOTE: 校验顺序与旧前端一致，先出现的错误先提示
export function validateFinalize(
  draft: Pick<RollDraft, 'draft_id' | 'token'> | null | undefined,
  form: Pick<ManualForm, 'name' | 'gender'>,
  remaining: number,
): string | null {
  if (!draft?.draft_id || !draft?.token) return '请先投掷并锁定属性'
  if (!form.name.trim()) return '姓名不能为空'
  if (!['男', '女'].includes(form.gender)) return '请选择性别'
  if (remaining < 0) return '技能点已超出预算'
  return null
}

export function buildFinalizePayload(
  draft: RollDraft,
  form: ManualForm,
  skills: SkillValues,
  defaults: SkillDefaults,
): FinalizePayload {
  const normalized: Record<string, number> = {}
  for (const [name, value] of Object.entries(skills)) normalized[name] = clampSkill(name, value, defaults, draft.stats)
  return {
    draft_id: draft.draft_id,
    token: draft.token,
    name: form.name,
    gender: form.gender,
    occupation: form.occupation,
    birthplace: form.birthplace,
    residence: form.residence,
    creation_hint: form.creation_hint,
    skills: normalized,
  }
}
