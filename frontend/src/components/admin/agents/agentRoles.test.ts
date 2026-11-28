import { describe, expect, test } from 'vitest'
import type { AgentConfigRow } from '@/api/adminTypesCore'
import {
  EXPECTED_ROLES,
  ROLE_DEFAULTS,
  agentDesc,
  agentLabel,
  buildAgentForms,
  isHiddenRole,
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
    thinking_level: 'high',
    thinking_budget_tokens: 0,
    is_active: true,
    ...over,
  }
}

describe('角色表', () => {
  test('预期角色共 18 个且不含已下线的 scripter、npc、npc_nsfw', () => {
    expect(EXPECTED_ROLES).toHaveLength(18)
    expect(EXPECTED_ROLES).not.toContain('scripter')
    expect(EXPECTED_ROLES).not.toContain('npc')
    expect(EXPECTED_ROLES).not.toContain('npc_nsfw')
    expect(EXPECTED_ROLES[0]).toBe('director')
    expect(EXPECTED_ROLES[1]).toBe('director_nsfw')
    expect(EXPECTED_ROLES.at(-1)).toBe('companion')
  })

  test('每个预期角色都同时有名称和描述', () => {
    for (const role of EXPECTED_ROLES) {
      expect(agentLabel(role), role).not.toBe(role)
      expect(agentDesc(role), role).not.toBe('')
    }
  })

  test('名称、描述与旧 admin.js 文案一致（抽样）', () => {
    expect(agentLabel('director')).toBe('🎬 Director')
    expect(agentLabel('writer_nsfw')).toBe('🔞 NSFW Writer')
    expect(agentLabel('diffusion_translator')).toBe('🏷️ Diffusion Translator')
    expect(agentLabel('compiler')).toBe('🛠️ Compiler')
    expect(agentDesc('director')).toBe('场景导演 — 决策工具调用序列')
    expect(agentDesc('qa_guard')).toBe('质量审查 — 审查模组可玩性、规则合规与线索设计')
  })

  test('embedding 角色的文案说明只支持 OpenAI 兼容接口与退回标签匹配', () => {
    expect(agentLabel('embedding')).toContain('Embedding')
    expect(agentDesc('embedding')).toContain('/embeddings')
    expect(agentDesc('embedding')).toContain('Anthropic')
    expect(agentDesc('embedding')).toContain('标签匹配')
    expect(agentDesc('embedding')).toContain('重算')
    expect(agentDesc('embedding')).toContain('text-embedding-3-small')
  })

  test('systemone 角色的文案说明第三方、拦截和未启用时放行', () => {
    expect(agentLabel('systemone')).toContain('System One')
    expect(agentDesc('systemone')).toContain('TypeSafe')
    expect(agentDesc('systemone')).toContain('第三方')
    expect(agentDesc('systemone')).toContain('放行')
    expect(agentDesc('systemone')).toContain('jev-latest')
    expect(agentDesc('systemone')).toContain('gemini-decision-1')
    expect(agentDesc('systemone')).toContain('API Key')
  })

  test('director_nsfw 角色的文案说明触发条件、共用提示词、依赖 systemone 和未启用时的行为', () => {
    expect(agentLabel('director_nsfw')).toBe('🔞 NSFW Director')
    expect(agentDesc('director_nsfw')).toContain('NSFW')
    expect(agentDesc('director_nsfw')).toContain('代替 Director')
    expect(agentDesc('director_nsfw')).toContain('共用提示词、工具和记忆')
    expect(agentDesc('director_nsfw')).toContain('systemone')
    expect(agentDesc('director_nsfw')).toContain('一律由 Director 执行')
    expect(agentDesc('director_nsfw')).toContain('上下文窗口')
  })

  test('systemone 角色的文案同时说明色情分类与发给第三方的内容', () => {
    expect(agentDesc('systemone')).toContain('director_nsfw')
    expect(agentDesc('systemone')).toContain('KP 的公开发言')
    expect(agentDesc('systemone')).toContain('已装备物品')
  })

  test('companion 角色的文案说明 AI 卡池、function calling 和未启用时的行为', () => {
    expect(agentLabel('companion')).toContain('AI 陪玩')
    expect(agentDesc('companion')).toContain('AI 卡池')
    expect(agentDesc('companion')).toContain('function calling')
    expect(agentDesc('companion')).toContain('未启用')
  })

  test('未知角色：名称回落为角色名，描述为空', () => {
    expect(agentLabel('lore_researcher')).toBe('lore_researcher')
    expect(agentDesc('lore_researcher')).toBe('')
  })

  test('原型属性名不会被当成角色', () => {
    expect(agentLabel('constructor')).toBe('constructor')
    expect(agentDesc('toString')).toBe('')
    expect(agentDesc('__proto__')).toBe('')
  })

  test('已下线角色没有名称、描述和默认值，也不是隐藏角色以外的可配置项', () => {
    for (const r of ['npc', 'npc_nsfw']) {
      expect(ROLE_DEFAULTS[r], r).toBeUndefined()
      expect(agentLabel(r), r).toBe(r)
      expect(agentDesc(r), r).toBe('')
      expect(isHiddenRole(r), r).toBe(true)
    }
    expect(isHiddenRole('scripter')).toBe(true)
    expect(isHiddenRole('lawyer')).toBe(false)
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
    expect(ROLE_DEFAULTS.systemone).toStrictEqual({
      max_tokens: 0,
      temperature: 0,
      model_name: 'gemini-decision-1',
      thinking_level: 'none',
      is_active: false,
    })
    expect(ROLE_DEFAULTS.director_nsfw).toStrictEqual({ max_tokens: 1500, temperature: 0.7, context_window: 0, is_active: false })
    for (const r of ['director_nsfw', 'writer_nsfw', 'chore', 'painter_nsfw', 'painter_hq', 'dramaturg', 'embedding', 'systemone', 'companion']) {
      expect(ROLE_DEFAULTS[r]?.is_active, r).toBe(false)
    }
    expect(ROLE_DEFAULTS.chore).toStrictEqual({ max_tokens: 2200, temperature: 0.7, is_active: false })
    expect(ROLE_DEFAULTS.companion).toStrictEqual({ max_tokens: 1500, temperature: 0.7, is_active: false })
  })
})

describe('buildAgentForms', () => {
  test('空列表：18 个角色全部按默认值补齐，顺序同角色表', () => {
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
      is_active: true,
    })
    const painter = forms.find((f) => f.role === 'painter')!
    expect(painter).toMatchObject({ model_name: 'dall-e-3', thinking_level: 'none', is_active: false, max_tokens: 0 })
    const companion = forms.at(-1)!
    expect(companion).toMatchObject({ role: 'companion', max_tokens: 1500, temperature: 0.7, is_active: false })
    const systemone = forms.at(-2)!
    expect(systemone).toMatchObject({ role: 'systemone', model_name: 'gemini-decision-1', thinking_level: 'none', is_active: false, max_tokens: 0 })
    const embedding = forms.at(-3)!
    expect(embedding).toMatchObject({ role: 'embedding', model_name: 'text-embedding-3-small', thinking_level: 'none', is_active: false, max_tokens: 0 })
  })

  test('null / undefined 入参等同空列表', () => {
    expect(buildAgentForms(null)).toHaveLength(18)
    expect(buildAgentForms(undefined)).toHaveLength(18)
  })

  test('库里已有的行排在前面，缺行的角色补在后面；provider_config_id 为 null 时变空串', () => {
    const forms = buildAgentForms([row('writer', { id: 7, provider_config_id: 3 }), row('director', { id: 2 })])
    expect(forms.slice(0, 2).map((f) => [f.role, f.id, f.provider_config_id])).toStrictEqual([
      ['writer', 7, 3],
      ['director', 2, ''],
    ])
    expect(forms).toHaveLength(18)
    expect(forms.slice(2).map((f) => f.role)).not.toContain('writer')
    expect(forms[2]!.id).toBe('new-director_nsfw')
  })

  test('scripter 行被丢弃，不会为它补默认行；未知角色原样保留', () => {
    const forms = buildAgentForms([row('scripter'), row('lore_researcher', { id: 9 })])
    expect(forms.map((f) => f.role)).not.toContain('scripter')
    expect(forms[0]!.role).toBe('lore_researcher')
    expect(forms).toHaveLength(19)
  })

  test('已下线的 npc / npc_nsfw 旧行被丢弃，不会显示也不会补默认行', () => {
    const forms = buildAgentForms([row('npc'), row('npc_nsfw'), row('lawyer')])
    const roles = forms.map((f) => f.role)
    expect(roles).not.toContain('npc')
    expect(roles).not.toContain('npc_nsfw')
    expect(roles[0]).toBe('lawyer')
    expect(forms).toHaveLength(18)
  })

  test('同一角色重复时后者覆盖前者，位置保持首次出现处', () => {
    const forms = buildAgentForms([row('writer', { id: 1, model_name: 'a' }), row('lawyer', { id: 2 }), row('writer', { id: 3, model_name: 'b' })])
    expect(forms.slice(0, 2).map((f) => [f.role, f.id, f.model_name])).toStrictEqual([
      ['writer', 3, 'b'],
      ['lawyer', 2, 'gpt-4o'],
    ])
  })
})
