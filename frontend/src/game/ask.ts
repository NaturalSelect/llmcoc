// NOTE: 点名问题卡的纯逻辑。状态（勾选、修改中、输入内容）放在调用方持有的 AskDraft 里，这里的函数只读 room 和 draft，不碰 DOM
import * as RS from './roomState'
import type { Room } from './roomState'

export const CHAT_MAX_CHARS = 2000

export interface AskDraft {
  pick: string[]
  editing: boolean
  input: string
}

// 'card' 作答中，'done' 已提交后收成一行摘要；没有被点名时为空串
export type AskView = '' | 'card' | 'done'

export function createAskDraft(): AskDraft {
  return { pick: [], editing: false, input: '' }
}

export function resetAskDraft(draft: AskDraft): void {
  draft.pick = []
  draft.editing = false
  draft.input = ''
}

// NOTE: 周期一开始，上一个问题就已经收口；残留的勾选和草稿不能带到下一次点名。页面在控制器的 onFrame 回调里调用它
export function resetDraftOnFrame(draft: AskDraft, frame: { type: string }): void {
  if (frame.type === 'floor_start') resetAskDraft(draft)
}

// NOTE: 只看「被点名且 KP 没在发言」，不看连接：断线时卡片和已写的内容保留，只是提交被 askLocked 挡住
export function askOpen(room: Room | null): room is Room {
  return !!room && room.status === 'playing' && !room.floor.running && !!room.spotlight.askedMe
}

export function askView(room: Room | null, draft: AskDraft): AskView {
  if (!askOpen(room)) return ''
  return room.me.submitted && !draft.editing ? 'done' : 'card'
}

// NOTE: 问题卡挂在最近一条点名消息下面；找不到点名消息，或它之后已有玩家发言时返回 null，由页面把卡片放到消息流末尾
export function askAnchor(room: Room | null): number | null {
  if (!askOpen(room)) return null
  for (let i = room.messages.length - 1; i >= 0; i--) {
    const msg = room.messages[i]
    if (msg.kind === 'player') return null
    if (msg.kind === 'ask') return msg.id
  }
  return null
}

export function askLocked(room: Room | null): boolean {
  return askOpen(room) && !RS.gates(room).canSpeak
}

export function askLockNote(room: Room | null): string {
  if (!room || !askLocked(room)) return ''
  return room.conn === 'reconnecting'
    ? '连接中断，已保留你写的内容，恢复后即可提交'
    : '正在同步房间状态，稍后即可提交'
}

export function askTag(room: Room | null): string {
  const names = room?.spotlight.names || []
  return names.length > 1 ? 'KP 点名：' + names.join('、') + '，请各自回应' : 'KP 向你提问'
}

// 推荐行动取最近一条点名消息里的选项（KP 只在玩家问过建议时才给）；之后出现过玩家行动就不再推荐
export function askOptions(room: Room | null): string[] {
  if (!askOpen(room)) return []
  for (let i = room.messages.length - 1; i >= 0; i--) {
    const msg = room.messages[i]
    if (msg.kind === 'player') return []
    if (msg.kind !== 'ask') continue
    const options = msg.meta?.options
    if (!Array.isArray(options)) return []
    return options.map((o) => String(o || '').trim()).filter(Boolean)
  }
  return []
}

// 勾选的推荐行动按选项顺序在前，自己写的内容在后，一起作为这次回应
export function askAnswer(options: string[], picked: string[], custom: string): string {
  const chosen = options.filter((o) => picked.includes(o))
  const own = String(custom || '').trim()
  return (own ? [...chosen, own] : chosen).join('\n').trim()
}

export function draftAnswer(room: Room | null, draft: AskDraft): string {
  return askAnswer(askOptions(room), draft.pick, draft.input)
}

export function askCharCount(text: string): number {
  return [...text].length
}

export function toggleOption(room: Room | null, draft: AskDraft, option: string): void {
  if (askView(room, draft) !== 'card' || askLocked(room)) return
  const i = draft.pick.indexOf(option)
  if (i >= 0) draft.pick.splice(i, 1)
  else draft.pick.push(option)
}

// 把已提交的内容放回输入框，再次提交时服务端会覆盖之前的内容
export function startEdit(room: Room | null, draft: AskDraft): void {
  if (!askOpen(room)) return
  draft.editing = true
  draft.pick = []
  draft.input = room.me.draft || ''
}

export function cancelEdit(draft: AskDraft): void {
  draft.editing = false
  draft.pick = []
  draft.input = ''
}

// NOTE: 在光标处插入「」并把光标放进括号内；有选中文字时直接包住选中内容
export function insertDialogueQuotes(
  value: string,
  start: number,
  end: number,
): { value: string; caret: number } {
  const selected = value.slice(start, end)
  return {
    value: value.slice(0, start) + '「' + selected + '」' + value.slice(end),
    caret: selected ? end + 2 : start + 1,
  }
}
