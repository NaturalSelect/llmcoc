import { describe, expect, test } from 'vitest'
import type { AgentConfigRow } from '@/api/adminTypesCore'
import {
  EXPECTED_ROLES,
  ROLE_DEFAULTS,
  agentDefaultHint,
  agentDesc,
  agentLabel,
  buildAgentForms,
} from './agentRoles'

function row(role: string, over: Partial<AgentConfigRow> = {}): AgentConfigRow {
  return {
    id: 1,
    role,
    provider_config_id: null,
    model_name: 'gpt-4o',
    max_tokens: 1024,
    temperature: 0.7,
    disable_temperature: false,
    image_via_chat: false,
    diffusion_mode: false,
    context_window: 0,
    with_jailbreak: false,
    system_prompt: '',
    thinking_level: 'high',
    thinking_budget_tokens: 0,
    is_active: true,
    ...over,
  }
}

describe('角色表', () => {
  test('预期角色共 16 个且不含已下线的 scripter', () => {
    expect(EXPECTED_ROLES).toHaveLength(16)
    expect(EXPECTED_ROLES).not.toContain('scripter')
    expect(EXPECTED_ROLES[0]).toBe('director')
    expect(EXPECTED_ROLES.at(-1)).toBe('embedding')
  })

  test('每个预期角色都同时有名称、描述和占位提示', () => {
    for (const role of EXPECTED_ROLES) {
      expect(agentLabel(role), role).not.toBe(role)
      expect(agentDesc(role), role).not.toBe('')
      expect(agentDefaultHint(role), role).not.toBe('留空使用默认')
    }
  })

  test('名称、描述、提示与旧 admin.js 文案一致（抽样）', () => {
    expect(agentLabel('director')).toBe('🎬 Director')
    expect(agentLabel('writer_nsfw')).toBe('🔞 NSFW Writer')
    expect(agentLabel('diffusion_translator')).toBe('🏷️ Diffusion Translator')
    expect(agentLabel('compiler')).toBe('🛠️ Compiler')
    expect(agentDesc('director')).toBe('场景导演 — 决策工具调用序列')
    expect(agentDesc('qa_guard')).toBe('质量审查 — 审查模组可玩性、规则合规与线索设计')
    expect(agentDefaultHint('lawyer')).toBe('留空使用内置 Lawyer 提示词（规则查阅模式）')
    expect(agentDefaultHint('dramaturg')).toBe('留空使用内置 Dramaturg 提示词（节奏顾问）；不配置则Director自行判断节奏（默认关闭）')
  })

  test('embedding 角色的文案说明只支持 OpenAI 兼容接口与退回标签匹配', () => {
    expect(agentLabel('embedding')).toContain('Embedding')
    expect(agentDesc('embedding')).toContain('/embeddings')
    expect(agentDesc('embedding')).toContain('Anthropic')
    expect(agentDesc('embedding')).toContain('标签匹配')
    expect(agentDesc('embedding')).toContain('重算')
    expect(agentDefaultHint('embedding')).toContain('text-embedding-3-small')
  })

  test('未知角色：名称回落为角色名，描述为空，提示为默认文案', () => {
    expect(agentLabel('lore_researcher')).toBe('lore_researcher')
    expect(agentDesc('lore_researcher')).toBe('')
    expect(agentDefaultHint('lore_researcher')).toBe('留空使用默认')
  })

  test('原型属性名不会被当成角色', () => {
    expect(agentLabel('constructor')).toBe('constructor')
    expect(agentDesc('toString')).toBe('')
    expect(agentDefaultHint('__proto__')).toBe('留空使用默认')
  })

  test('默认值与旧 roleDefaults 一致（抽样）', () => {
    expect(ROLE_DEFAULTS.director).toStrictEqual({ max_tokens: 1500, temperature: 0.7, context_window: 0 })
    expect(ROLE_DEFAULTS.writer).toStrictEqual({ max_tokens: 800, temperature: 0.85 })
    expect(ROLE_DEFAULTS.compiler).toStrictEqual({ max_tokens: 4000, temperature: 0.15 })
    expect(ROLE_DEFAULTS.painter).toStrictEqual({
      max_tokens: 0,
      temperature: 0,
      model_name: 'dall-e-3',
      thinking_level: 'none',
      is_active: false,
    })
    expect(ROLE_DEFAULTS.embedding).toStrictEqual({
      max_tokens: 0,
      temperature: 0,
      model_name: 'text-embedding-3-small',
      thinking_level: 'none',
      is_active: false,
    })
    for (const r of ['writer_nsfw', 'npc_nsfw', 'painter_nsfw', 'painter_hq', 'dramaturg', 'embedding']) {
      expect(ROLE_DEFAULTS[r]?.is_active, r).toBe(false)
    }
  })
})

describe('buildAgentForms', () => {
  test('空列表：16 个角色全部按默认值补齐，顺序同角色表', () => {
    const forms = buildAgentForms([])
    expect(forms.map((f) => f.role)).toStrictEqual(EXPECTED_ROLES)
    const director = forms[0]!
    expect(director).toStrictEqual({
      id: 'new-director',
      role: 'director',
      provider_config_id: '',
      model_name: '',
      max_tokens: 1500,
      context_window: 0,
      temperature: 0.7,
      disable_temperature: false,
      image_via_chat: false,
      diffusion_mode: false,
      with_jailbreak: false,
      thinking_level: '',
      thinking_budget_tokens: 0,
      system_prompt: '',
      is_active: true,
    })
    const painter = forms.find((f) => f.role === 'painter')!
    expect(painter).toMatchObject({ model_name: 'dall-e-3', thinking_level: 'none', is_active: false, max_tokens: 0 })
    const embedding = forms.at(-1)!
    expect(embedding).toMatchObject({ role: 'embedding', model_name: 'text-embedding-3-small', thinking_level: 'none', is_active: false, max_tokens: 0 })
  })

  test('null / undefined 入参等同空列表', () => {
    expect(buildAgentForms(null)).toHaveLength(16)
    expect(buildAgentForms(undefined)).toHaveLength(16)
  })

  test('库里已有的行排在前面，缺行的角色补在后面；provider_config_id 为 null 时变空串', () => {
    const forms = buildAgentForms([row('writer', { id: 7, provider_config_id: 3 }), row('director', { id: 2 })])
    expect(forms.slice(0, 2).map((f) => [f.role, f.id, f.provider_config_id])).toStrictEqual([
      ['writer', 7, 3],
      ['director', 2, ''],
    ])
    expect(forms).toHaveLength(16)
    expect(forms.slice(2).map((f) => f.role)).not.toContain('writer')
    expect(forms[2]!.id).toBe('new-writer_nsfw')
  })

  test('scripter 行被丢弃，不会为它补默认行；未知角色原样保留', () => {
    const forms = buildAgentForms([row('scripter'), row('lore_researcher', { id: 9 })])
    expect(forms.map((f) => f.role)).not.toContain('scripter')
    expect(forms[0]!.role).toBe('lore_researcher')
    expect(forms).toHaveLength(17)
  })

  test('同一角色重复时后者覆盖前者，位置保持首次出现处', () => {
    const forms = buildAgentForms([row('npc', { id: 1, model_name: 'a' }), row('lawyer', { id: 2 }), row('npc', { id: 3, model_name: 'b' })])
    expect(forms.slice(0, 2).map((f) => [f.role, f.id, f.model_name])).toStrictEqual([
      ['npc', 3, 'b'],
      ['lawyer', 2, 'gpt-4o'],
    ])
  })
})
