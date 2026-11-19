// NOTE: 将房间消息流整理为可下载的 Markdown 战报；纯函数，不依赖 DOM 或浏览器 API
import type { Session } from '@/api/types'
import { describeEvent, eventsOf } from './events'
import { msgKind } from './messages'
import type { RoomMessage } from './roomState'

export interface ReplayExportOptions {
  includeEvents?: boolean
  writerTextOf?: (msg: RoomMessage) => string
}

function formatEventLine(msg: RoomMessage): string[] {
  const lines: string[] = []
  for (const ev of eventsOf(msg)) {
    const view = describeEvent(ev)
    const parts = [view.text]
    if (view.value) parts.push(`**${view.value}**`)
    if (view.note) parts.push(`（${view.note}）`)
    lines.push(`> - ${parts.join(' ')}`)
  }
  return lines
}

export function buildReplayMarkdown(
  session: Session | null | undefined,
  messages: readonly RoomMessage[],
  opts: ReplayExportOptions = {},
): string {
  const includeEvents = opts.includeEvents !== false
  const title = (typeof session?.name === 'string' && session.name.trim()) || '跑团战报'
  const scenarioName = (session?.scenario as { name?: string } | null | undefined)?.name || ''
  const out: string[] = [`# ${title}`]

  const metaLines: string[] = []
  if (scenarioName) metaLines.push(`- **模组**：${scenarioName}`)
  const players = session?.players ?? []
  if (players.length > 0) {
    const names = players
      .map((p) => {
        const cardName = p.character_card?.name || '未命名调查员'
        const userName = p.user?.username ? `（@${p.user.username}）` : ''
        return `${cardName}${userName}`
      })
      .join('、')
    metaLines.push(`- **调查员**：${names}`)
  }
  metaLines.push(`- **导出模式**：${includeEvents ? '完整战报（含骰点与结算事件）' : '小说模式（纯剧情叙事）'}`)
  out.push('', ...metaLines, '', '---')

  for (const msg of messages) {
    const kind = msgKind(msg)
    if (kind === 'event') {
      if (!includeEvents) continue
      const lines = formatEventLine(msg)
      if (lines.length) out.push('', ...lines)
      continue
    }
    if (kind === 'image') continue

    const rawText = kind === 'writer' && opts.writerTextOf ? opts.writerTextOf(msg) : msg.content || ''
    const text = rawText.trim()
    if (!text) continue

    if (kind === 'writer') {
      out.push('', text)
    } else if (kind === 'kp') {
      const whisper = msg.visibility === 'private' ? '（密语）' : ''
      out.push('', `**【KP${whisper}】**`, '', text)
    } else if (kind === 'player') {
      const speaker = (typeof msg.sender_name === 'string' && msg.sender_name.trim()) || '调查员'
      out.push('', `**【${speaker}】**：${text}`)
    } else {
      out.push('', `> *系统：${text}*`)
    }
  }

  return out.join('\n').trim() + '\n'
}
