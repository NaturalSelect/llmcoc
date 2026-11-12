import { describe, expect, test } from 'vitest'
import type { AgentFormState } from '@/api/adminTypesCore'
import {
  buildSavePayload,
  hasContextWindow,
  isEmbeddingRole,
  isPainterRole,
  pingFailureMessage,
  pingLoadingKey,
  pingSuccessMessage,
  planPing,
  savedMessage,
} from './agentActions'

function form(over: Partial<AgentFormState> = {}): AgentFormState {
  return {
    id: 1,
    role: 'director',
    provider_config_id: 4,
    model_name: ' gpt-4o ',
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
    ...over,
  }
}

describe('角色判定', () => {
  test('painter 系与上下文窗口角色', () => {
    expect(isPainterRole('painter')).toBe(true)
    expect(isPainterRole('painter_nsfw')).toBe(true)
    expect(isPainterRole('painter_hq')).toBe(true)
    expect(isPainterRole('writer')).toBe(false)
    expect(['director', 'writer', 'writer_nsfw'].every(hasContextWindow)).toBe(true)
    expect(hasContextWindow('npc')).toBe(false)
  })

  test('embedding 只匹配自身，不被当成画图角色', () => {
    expect(isEmbeddingRole('embedding')).toBe(true)
    expect(isEmbeddingRole('painter')).toBe(false)
    expect(isEmbeddingRole('lawyer')).toBe(false)
    expect(isPainterRole('embedding')).toBe(false)
    expect(hasContextWindow('embedding')).toBe(false)
  })
})

describe('buildSavePayload', () => {
  test('未绑定提供商（空串）提交 null，缺省数值归零，字符串原样提交', () => {
    const p = buildSavePayload(form({ provider_config_id: '', context_window: '', thinking_budget_tokens: '', system_prompt: '  x ' }))
    expect(p).toStrictEqual({
      provider_config_id: null,
      model_name: ' gpt-4o ',
      max_tokens: 1500,
      context_window: 0,
      temperature: 0.7,
      disable_temperature: false,
      image_via_chat: false,
      diffusion_mode: false,
      with_jailbreak: false,
      system_prompt: '  x ',
      thinking_level: '',
      thinking_budget_tokens: 0,
      is_active: true,
    })
  })

  test('保留已绑定的提供商与各开关；清空的 max_tokens 原样提交给后端校验', () => {
    const p = buildSavePayload(
      form({ provider_config_id: 9, disable_temperature: true, with_jailbreak: true, thinking_level: 'xhigh', thinking_budget_tokens: 2048, max_tokens: '', is_active: false }),
    )
    expect(p).toMatchObject({
      provider_config_id: 9,
      disable_temperature: true,
      with_jailbreak: true,
      thinking_level: 'xhigh',
      thinking_budget_tokens: 2048,
      max_tokens: '',
      is_active: false,
    })
  })

  test('保存成功文案带角色名称', () => {
    expect(savedMessage('writer')).toBe('✍️ Writer 配置已保存')
  })
})

describe('planPing', () => {
  test('未选提供商 / 未填模型名给出旧文案，提供商优先判断', () => {
    expect(planPing(form({ provider_config_id: '', model_name: '' }))).toStrictEqual({ ok: false, error: '请先选择 Provider' })
    expect(planPing(form({ model_name: '   ' }))).toStrictEqual({ ok: false, error: '请先填写模型名称' })
  })

  test('文本角色：模型名去空格，不带 mode', () => {
    expect(planPing(form())).toStrictEqual({
      ok: true,
      providerId: 4,
      role: 'director',
      isPainter: false,
      key: 'director:4',
      payload: { model_name: 'gpt-4o', role: 'director' },
    })
  })

  test('画图角色：附带 image 模式与两个开关', () => {
    const plan = planPing(form({ role: 'painter_nsfw', provider_config_id: 2, model_name: 'dall-e-3', diffusion_mode: true }))
    expect(plan).toMatchObject({
      ok: true,
      isPainter: true,
      key: 'painter_nsfw:2',
      payload: { model_name: 'dall-e-3', role: 'painter_nsfw', mode: 'image', image_via_chat: false, diffusion_mode: true },
    })
  })

  test('embedding 角色：附带 embedding 模式，不带画图开关', () => {
    const plan = planPing(form({ role: 'embedding', provider_config_id: 3, model_name: ' text-embedding-3-small ' }))
    expect(plan).toStrictEqual({
      ok: true,
      providerId: 3,
      role: 'embedding',
      isPainter: false,
      key: 'embedding:3',
      payload: { model_name: 'text-embedding-3-small', role: 'embedding', mode: 'embedding' },
    })
  })

  test('提供商 ID 无法转成数字时视为未选', () => {
    expect(planPing(form({ provider_config_id: 'abc' as unknown as number }))).toStrictEqual({ ok: false, error: '请先选择 Provider' })
  })
})

describe('pingLoadingKey 与提示文案', () => {
  test('未绑定时键为 role:0', () => {
    expect(pingLoadingKey({ role: 'npc', provider_config_id: '' })).toBe('npc:0')
    expect(pingLoadingKey({ role: 'npc', provider_config_id: 5 })).toBe('npc:5')
  })

  test('成功与失败文案区分画图角色', () => {
    expect(pingSuccessMessage('painter', 120)).toBe('Painter 图片模型连通正常，延迟 120 ms')
    expect(pingSuccessMessage('lawyer', 80)).toBe('⚖️ Lawyer 模型连通正常，延迟 80 ms')
    expect(pingFailureMessage('painter_nsfw', 'boom')).toBe('Painter 图片模型测试失败：boom')
    expect(pingFailureMessage('lawyer', 'boom')).toBe('⚖️ Lawyer 模型测试失败：boom')
  })

  test('embedding 成功文案带维度与延迟；响应没带维度时只显示延迟', () => {
    expect(pingSuccessMessage('embedding', 95, 1536)).toBe('Embedding 嵌入模型连通正常，向量维度 1536，延迟 95 ms')
    expect(pingSuccessMessage('embedding', 95)).toBe('Embedding 嵌入模型连通正常，延迟 95 ms')
    expect(pingSuccessMessage('embedding', 95, 0)).toBe('Embedding 嵌入模型连通正常，延迟 95 ms')
    expect(pingSuccessMessage('lawyer', 80, 1536)).toBe('⚖️ Lawyer 模型连通正常，延迟 80 ms')
    expect(pingFailureMessage('embedding', 'boom')).toBe('Embedding 嵌入模型测试失败：boom')
  })
})
