import type { AgentFormState } from '@/api/adminTypesCore'
import { agentLabel } from './agentRoles'

// NOTE: 保存/测试模型的请求体构造，字段与旧 admin.js 的 saveAgent / pingAgentModel 逐项一致

export function isPainterRole(role: string): boolean {
  return role === 'painter' || role === 'painter_nsfw' || role === 'painter_hq'
}

export function isEmbeddingRole(role: string): boolean {
  return role === 'embedding'
}

export function isSystemOneRole(role: string): boolean {
  return role === 'systemone'
}

// NOTE: 只有 Director 按上下文窗口触发总结，其余角色不显示该输入框
export function hasContextWindow(role: string): boolean {
  return role === 'director'
}

export function buildSavePayload(ag: AgentFormState) {
  return {
    provider_config_id: ag.provider_config_id || null,
    model_name: ag.model_name,
    max_tokens: ag.max_tokens,
    context_window: ag.context_window || 0,
    temperature: ag.temperature,
    disable_temperature: ag.disable_temperature || false,
    image_via_chat: ag.image_via_chat || false,
    diffusion_mode: ag.diffusion_mode || false,
    with_jailbreak: ag.with_jailbreak || false,
    system_prompt: ag.system_prompt,
    thinking_level: ag.thinking_level || '',
    thinking_budget_tokens: ag.thinking_budget_tokens || 0,
    is_active: ag.is_active,
  }
}

export function savedMessage(role: string): string {
  return agentLabel(role) + ' 配置已保存'
}

export function pingLoadingKey(ag: Pick<AgentFormState, 'role' | 'provider_config_id'>): string {
  return ag.role + ':' + Number(ag.provider_config_id || 0)
}

export interface PingPayload {
  model_name: string
  role: string
  mode?: 'image' | 'embedding' | 'systemone'
  image_via_chat?: boolean
  diffusion_mode?: boolean
}

export type PingPlan =
  | { ok: false; error: string }
  | { ok: true; providerId: number; role: string; isPainter: boolean; key: string; payload: PingPayload }

export function planPing(ag: AgentFormState): PingPlan {
  const providerId = ag.provider_config_id ? Number(ag.provider_config_id) : 0
  const modelName = (ag.model_name || '').trim()
  if (!providerId) return { ok: false, error: '请先选择 Provider' }
  if (!modelName) return { ok: false, error: '请先填写模型名称' }

  const role = ag.role || ''
  const isPainter = isPainterRole(role)
  const payload: PingPayload = { model_name: modelName, role }
  if (isPainter) {
    payload.mode = 'image'
    payload.image_via_chat = ag.image_via_chat || false
    payload.diffusion_mode = ag.diffusion_mode || false
  } else if (isEmbeddingRole(role)) {
    payload.mode = 'embedding'
  } else if (isSystemOneRole(role)) {
    payload.mode = 'systemone'
  }
  return { ok: true, providerId, role, isPainter, key: `${role}:${providerId}`, payload }
}

// NOTE: dimensions 只有嵌入模型的响应才带；缺失或非正数时不显示，避免出现「维度 undefined」
export function pingSuccessMessage(role: string, latencyMs: number, dimensions?: number): string {
  if (isPainterRole(role)) return `Painter 图片模型连通正常，延迟 ${latencyMs} ms`
  if (isEmbeddingRole(role)) {
    const dim = dimensions && dimensions > 0 ? `，向量维度 ${dimensions}` : ''
    return `Embedding 嵌入模型连通正常${dim}，延迟 ${latencyMs} ms`
  }
  if (isSystemOneRole(role)) return `System One 输入分类器连通正常，延迟 ${latencyMs} ms`
  return `${agentLabel(role)} 模型连通正常，延迟 ${latencyMs} ms`
}

export function pingFailureMessage(role: string, reason: string): string {
  let prefix = `${agentLabel(role)} 模型测试失败：`
  if (isPainterRole(role)) prefix = 'Painter 图片模型测试失败：'
  else if (isEmbeddingRole(role)) prefix = 'Embedding 嵌入模型测试失败：'
  else if (isSystemOneRole(role)) prefix = 'System One 输入分类器测试失败：'
  return prefix + reason
}
