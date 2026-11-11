// NOTE: 消息列表渲染辅助，全部是纯函数；CSS 类名等样式决定留给组件
import { lastEventTime } from './events'
import * as RS from './roomState'
import type { Room, RoomMessage, TimePoint } from './roomState'

export type MessageKind = 'legacy' | 'player' | 'system' | string

// 旧数据没有 kind：assistant 当作一整块 KP 回复，其余按 role 归类
export function msgKind(msg: Pick<RoomMessage, 'kind' | 'role'> | null | undefined): MessageKind {
  if (msg?.kind) return msg.kind
  if (msg?.role === 'assistant') return 'legacy'
  if (msg?.role === 'user') return 'player'
  return 'system'
}

export function msgLabel(msg: RoomMessage): string {
  const kind = msgKind(msg)
  if (kind === 'player') return msg.username || '玩家'
  if (kind === 'system') return '系统'
  if (kind === 'writer') return '剧情'
  if (kind === 'kp' || kind === 'ask') return '旁白'
  return 'KP'
}

export function isMyMessage(msg: RoomMessage, userId: number | string | null | undefined): boolean {
  return msgKind(msg) === 'player' && !!msg.user_id && Number(msg.user_id) === Number(userId)
}

// pending 的 writer 显示已流式收到的部分；其余状态直接用落库的正文
export function writerText(room: Room | null, msg: RoomMessage): string {
  if (msg.status === 'pending') return room?.streams?.[msg.id]?.text || ''
  return msg.content || ''
}

export function diceOf(msg: RoomMessage | null | undefined) {
  return RS.parseDice(msg?.meta?.dice)
}

export function timeOf(msg: RoomMessage | null | undefined): TimePoint | null {
  return RS.parseTimePoint(msg?.meta?.time_point)
}

// NOTE: advance_time 的事件比下一条 kp/ask 先到，游戏时钟要随它立即更新
export function currentGameTime(room: Room | null): TimePoint | null {
  const list = room?.messages || []
  for (let i = list.length - 1; i >= 0; i--) {
    const msg = list[i]
    const tp = msgKind(msg) === 'event' ? lastEventTime(msg) : msg?.meta?.time_point
    if (tp) return RS.parseTimePoint(tp)
  }
  return null
}

// NOTE: meta 的字段类型是 unknown，模板里不能直接读，统一在这里收窄
export function askNames(msg: RoomMessage): string[] {
  const ask = (msg.meta as { ask?: { names?: unknown } } | null | undefined)?.ask
  return Array.isArray(ask?.names) ? ask.names.map(String) : []
}

export function isEndedMessage(msg: RoomMessage): boolean {
  return !!(msg.meta as { ended?: unknown } | null | undefined)?.ended
}

export function messageError(msg: RoomMessage): string {
  return msg.meta?.error ? String(msg.meta.error) : ''
}

export interface DirectorTiming {
  elapsedMs: number
  steps: number
}

export function directorTiming(msg: RoomMessage): DirectorTiming | null {
  const elapsedMs = Number(msg.director_elapsed_ms) || 0
  if (!elapsedMs) return null
  return { elapsedMs, steps: Number(msg.director_steps) || 0 }
}

// NOTE: 只给没有 kind 的旧消息用：把历史上混在正文里的内部标签清掉
export function stripLegacyTags(input: unknown): string {
  const content = String(input || '')
  if (!content.includes('<')) return content.trim()
  return content
    .replace(/<image_ref\b[^>]*(?:\/>|>\s*<\/image_ref>)/gi, '')
    .replace(
      /<(ack|direction|response_options|writer_pending|image_data_url|dice|time_point)\b[^>]*>[\s\S]*?<\/\1>/gi,
      '',
    )
    .replace(
      /<(ack|direction|response_options|writer_pending|image_data_url|dice|time_point)\b[^>]*>[\s\S]*$/gi,
      '',
    )
    .trim()
}
