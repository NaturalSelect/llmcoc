// @vitest-environment node
import { describe, expect, test } from 'vitest'
import type { RollDraft } from '@/api/characterTypes'
import {
  aiPayload,
  applyRoll,
  buildFinalizePayload,
  clampSkill,
  emptyAiForm,
  emptyManualForm,
  lockSkills,
  remainingPoints,
  spentPoints,
  validateFinalize,
  validateRollAge,
  visibleSkillNames,
} from './manualCreate'

const stats = { edu: 70, dex: 55, int: 60 }
const defaults = { 母语: 0, 闪避: 0, 侦查: 25, 聆听: 20, 克苏鲁神话: 0 }
const draft = (over: Partial<RollDraft> = {}): RollDraft => ({
  draft_id: 7,
  token: 'tok',
  stats,
  skill_defaults: defaults,
  skill_budget: { occupation: 280, interest: 120, total: 400 },
  ...over,
})

describe('年龄校验', () => {
  test('15-90 之间通过，边界含端点', () => {
    expect(validateRollAge(15)).toBeNull()
    expect(validateRollAge(90)).toBeNull()
    expect(validateRollAge('25')).toBeNull()
  })

  test('越界、空值、非数字都报错', () => {
    for (const v of [14, 91, 0, '', null, undefined, 'abc']) expect(validateRollAge(v)).toBe('年龄必须在15-90之间')
  })
})

describe('技能默认值与锁定', () => {
  test('母语取 EDU，闪避取 DEX/2 向下取整，不改入参', () => {
    const out = lockSkills(defaults, stats)
    expect(out['母语']).toBe(70)
    expect(out['闪避']).toBe(27)
    expect(defaults['母语']).toBe(0)
  })

  test('没有属性时原样返回', () => {
    expect(lockSkills(defaults, undefined)).toEqual(defaults)
  })

  test('技能清单过滤掉克苏鲁神话；服务端没给清单时用默认值的键', () => {
    expect(visibleSkillNames(['侦查', '克苏鲁神话'], defaults)).toEqual(['侦查'])
    expect(visibleSkillNames(null, { 侦查: 1, 克苏鲁神话: 0 })).toEqual(['侦查'])
  })

  test('投掷后：预算、锁定值、清单和初始技能值', () => {
    const out = applyRoll(draft(), { defaults: {}, names: [] })
    expect(out.budget.total).toBe(400)
    expect(out.skills['母语']).toBe(70)
    expect(out.skills['闪避']).toBe(27)
    expect(out.names).toEqual(['侦查', '母语', '聆听', '闪避'].sort())
    expect(out.names).not.toContain('克苏鲁神话')
  })

  test('投掷后沿用已加载的技能清单；缺预算时回落为 0', () => {
    const out = applyRoll(draft({ skill_budget: undefined }), { defaults: {}, names: ['聆听', '侦查'] })
    expect(out.names).toEqual(['聆听', '侦查'])
    expect(out.budget).toEqual({ occupation: 0, interest: 0, total: 0 })
  })
})

describe('技能点预算', () => {
  const locked = lockSkills(defaults, stats)

  test('只统计高于基础值的部分，母语/闪避/克苏鲁神话不占预算', () => {
    const skills = { ...locked, 侦查: 50, 聆听: 20, 克苏鲁神话: 99 }
    expect(spentPoints(locked, skills)).toBe(25)
  })

  test('低于基础值不产生负数；输入框清空（空字符串）按 0 计', () => {
    expect(spentPoints(locked, { 侦查: 10 })).toBe(0)
    expect(spentPoints(locked, { 侦查: '' })).toBe(0)
    expect(spentPoints(locked, { 侦查: '40' })).toBe(15)
  })

  test('剩余点数可以为负', () => {
    expect(remainingPoints({ occupation: 0, interest: 0, total: 10 }, locked, { 侦查: 50 })).toBe(-15)
    expect(remainingPoints(null, locked, {})).toBe(0)
  })
})

describe('技能输入夹取', () => {
  const locked = lockSkills(defaults, stats)

  test('母语与闪避始终取服务器公式值', () => {
    expect(clampSkill('母语', 1, locked, stats)).toBe(70)
    expect(clampSkill('闪避', 99, locked, stats)).toBe(27)
  })

  test('不低于基础值、不高于 90、向下取整', () => {
    expect(clampSkill('侦查', 3, locked, stats)).toBe(25)
    expect(clampSkill('侦查', 120, locked, stats)).toBe(90)
    expect(clampSkill('侦查', 40.9, locked, stats)).toBe(40)
  })

  test('空值与非数字回落为基础值', () => {
    expect(clampSkill('侦查', '', locked, stats)).toBe(25)
    expect(clampSkill('侦查', 'abc', locked, stats)).toBe(25)
    expect(clampSkill('侦查', undefined, locked, stats)).toBe(25)
  })
})

describe('提交校验', () => {
  const ok = { name: '艾米', gender: '女' }

  test('校验顺序：草稿 → 姓名 → 性别 → 预算', () => {
    expect(validateFinalize(null, ok, 0)).toBe('请先投掷并锁定属性')
    expect(validateFinalize({ draft_id: 0, token: 't' }, ok, 0)).toBe('请先投掷并锁定属性')
    expect(validateFinalize({ draft_id: 1, token: '' }, ok, 0)).toBe('请先投掷并锁定属性')
    expect(validateFinalize(draft(), { name: '  ', gender: '女' }, 0)).toBe('姓名不能为空')
    expect(validateFinalize(draft(), { name: '艾米', gender: '' }, 0)).toBe('请选择性别')
    expect(validateFinalize(draft(), { name: '艾米', gender: '其他' }, 0)).toBe('请选择性别')
    expect(validateFinalize(draft(), ok, -1)).toBe('技能点已超出预算')
    expect(validateFinalize(draft(), ok, 0)).toBeNull()
  })

  test('同时有多个错误时先报靠前的', () => {
    expect(validateFinalize(null, { name: '', gender: '' }, -5)).toBe('请先投掷并锁定属性')
    expect(validateFinalize(draft(), { name: '', gender: '' }, -5)).toBe('姓名不能为空')
  })
})

describe('提交载荷', () => {
  const locked = lockSkills(defaults, stats)

  test('字段、技能夹取与锁定技能', () => {
    const form = { ...emptyManualForm(), name: '艾米', gender: '女', occupation: '记者', birthplace: '波士顿', residence: '阿卡姆', creation_hint: '家道中落' }
    const payload = buildFinalizePayload(draft(), form, { 侦查: '50', 聆听: 5, 母语: 1, 闪避: 2 }, locked)
    expect(payload).toEqual({
      draft_id: 7,
      token: 'tok',
      name: '艾米',
      gender: '女',
      occupation: '记者',
      birthplace: '波士顿',
      residence: '阿卡姆',
      creation_hint: '家道中落',
      skills: { 侦查: 50, 聆听: 20, 母语: 70, 闪避: 27 },
    })
  })

  test('技能值全部是 number，不会把输入框的字符串发给后端', () => {
    const payload = buildFinalizePayload(draft(), emptyManualForm(), { 侦查: '30' }, locked)
    expect(typeof payload.skills['侦查']).toBe('number')
  })
})

describe('AI 生成载荷', () => {
  test('年龄留空时不发送 age 字段（后端 age 是 int）', () => {
    const payload = aiPayload({ ...emptyAiForm(), name: '艾米' })
    expect('age' in payload).toBe(false)
    expect(payload).toEqual({ name: '艾米', race: '人类', gender: '', occupation: '', era: '', background: '' })
  })

  test('填了年龄则按数字发送；非有限数字视为未填', () => {
    expect(aiPayload({ ...emptyAiForm(), age: 28 }).age).toBe(28)
    expect('age' in aiPayload({ ...emptyAiForm(), age: Number.NaN })).toBe(false)
    expect('age' in aiPayload({ ...emptyAiForm(), age: null })).toBe(false)
  })

  test('表单初始值与旧前端一致', () => {
    expect(emptyAiForm()).toEqual({ name: '', race: '人类', gender: '', age: '', occupation: '', era: '', background: '' })
    expect(emptyManualForm().age).toBe(25)
  })
})
