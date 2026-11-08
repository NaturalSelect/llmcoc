// NOTE: 房间状态横幅与底部状态栏的文案。全部是 room 的纯函数
import * as RS from './roomState'
import type { Room } from './roomState'

export const STOP_TEXT: Record<string, string> = {
  host_stop: '房主打断',
  loop: '检测到重复操作',
  stall: '长时间没有进展',
  error: '出错',
  context: '上下文过长',
}

// tone: warn 连接问题 | me 轮到我 | info 其他进行中状态 | idle 没有进行中的事
// busy: KP 正在工作或在等人，状态栏圆点据此呼吸
export interface Banner {
  text: string
  tone: 'warn' | 'me' | 'info' | 'idle'
  busy: boolean
}

export interface SubmitPlayer {
  character_card?: { name?: string } | null
  user?: { username?: string } | null
}

// NOTE: 工具事件不算开场，与后端 hasKPStarted 只看 player/writer/kp/ask 保持一致
export function hasKPStarted(room: Room | null): boolean {
  return !!room && room.messages.some((m) => (m.floor ?? 0) > 0 && m.kind !== 'event')
}

export function continueLabel(room: Room | null): string {
  return hasKPStarted(room) ? '让 KP 继续' : '让 KP 开场'
}

export function showWaiting(room: Room | null): boolean {
  return (
    !!room &&
    room.status === 'playing' &&
    !room.floor.running &&
    room.waiting.total > 1 &&
    room.waiting.pending > 0 &&
    room.waiting.pending < room.waiting.total
  )
}

export function roomBanner(room: Room | null): Banner {
  if (!room) return { text: '', tone: 'idle', busy: false }
  const g = RS.gates(room)
  if (!g.live) {
    const text =
      room.conn === 'reconnecting'
        ? '连接中断，正在重连…'
        : room.conn === 'snapshot'
          ? '正在同步房间状态…'
          : '正在连接…'
    return { text, tone: 'warn', busy: true }
  }
  if (room.status === 'ended') return { text: '游戏已结束', tone: 'idle', busy: false }
  if (room.status !== 'playing') return { text: '等待房主开始游戏', tone: 'idle', busy: false }
  const sp = room.spotlight
  if (room.floor.running) return { text: room.kpStatus || 'KP 正在推进剧情…', tone: 'info', busy: true }
  if (room.floor.starting) return { text: 'KP 正在启动…', tone: 'info', busy: true }
  if (sp.stop) {
    const why = STOP_TEXT[sp.stop] || sp.stop
    return { text: 'KP 已暂停：' + why + '，等待房主让 KP 继续', tone: 'info', busy: false }
  }
  const names = (sp.names || []).join('、')
  if (sp.askedMe) {
    if (!room.me.submitted) return { text: 'KP 在等你回应', tone: 'me', busy: false }
    const waitFor = (room.waiting.pendingNames || []).join('、')
    return {
      text: waitFor ? '已提交行动，还在等 ' + waitFor : '已提交行动，等待 KP',
      tone: 'info',
      busy: true,
    }
  }
  if (sp.userIds.length > 0) {
    const w = room.waiting
    const progress = w.total > 1 ? '（' + (w.total - w.pending) + '/' + w.total + ' 已提交）' : ''
    return { text: 'KP 在等 ' + (names || '玩家') + ' 回应' + progress, tone: 'info', busy: true }
  }
  return {
    text: hasKPStarted(room) ? '等待房主让 KP 继续' : '等待房主让 KP 开场',
    tone: 'info',
    busy: false,
  }
}

// NOTE: 判断调查员在当前轮的提交状态，供调查员列表弹窗展示标记。
// 匹配时 trim 规范化，与后端 sessionPlayerDisplayName 逻辑一致。
export function playerTurnSubmitState(
  room: Room | null,
  player: SubmitPlayer | null | undefined,
): 'submitted' | 'pending' | null {
  if (!room || !showWaiting(room)) return null
  const name = (player?.character_card?.name || player?.user?.username || '').trim()
  if (!name) return null
  if ((room.waiting.submittedNames || []).some((n) => String(n).trim() === name)) return 'submitted'
  if ((room.waiting.pendingNames || []).some((n) => String(n).trim() === name)) return 'pending'
  return null
}
