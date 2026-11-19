// NOTE: 点名问题卡的纯逻辑。状态（勾选、修改中、输入内容）放在调用方持有的 AskDraft 里，这里的函数只读 room 和 draft，不碰 DOM
import type { CharacterCard } from '@/api/types'
import { eventsOf } from './events'
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

export interface BurnableCheck {
  skill: string
  roll: number
  need: number
  target: number
  level?: string
  cost: number
  affordable: boolean
  pushable: boolean
}

const NON_BURNABLE_SKILLS = ['理智', 'san', '幸运', 'luck']
const COMBAT_SKILL_HINTS = ['格斗', '斗殴', '射击', '闪避', '反击', '手枪', '步枪', '霰弹枪', '投掷', '弓箭', '近战']

function matchesAnyKeyword(skill: string, keywords: string[]): boolean {
  const s = skill.trim().toLowerCase()
  return keywords.some((k) => s.includes(k))
}

// NOTE: 扫描最近一次玩家行动之后的 KP 结算事件，找出本调查员最近一次可燃烧幸运的失败检定（不含大失败、理智、幸运和已扣幸运后的检定）。
export function findBurnableFailedCheck(
  room: Room | null,
  charName: string | undefined,
  currentLuck: number,
): BurnableCheck | null {
  if (!askOpen(room) || !charName) return null
  let luckSpentAfter = false
  for (let i = room.messages.length - 1; i >= 0; i--) {
    const msg = room.messages[i]
    if (msg.kind === 'player') break
    if (msg.kind !== 'event') continue
    const evs = eventsOf(msg)
    for (let j = evs.length - 1; j >= 0; j--) {
      const ev = evs[j]
      if (ev.type === 'stat' && ev.field === 'luck' && ev.character === charName && typeof ev.from === 'number' && typeof ev.to === 'number' && ev.to < ev.from) {
        luckSpentAfter = true
        continue
      }
      if (ev.type !== 'dice' || ev.hidden || ev.character !== charName) continue
      if (ev.result !== '失败' || !ev.skill) continue
      if (matchesAnyKeyword(ev.skill, NON_BURNABLE_SKILLS)) continue
      const need = typeof ev.need === 'number' && ev.need > 0 ? ev.need : ev.target || 0
      const roll = ev.roll || 0
      if (need <= 0 || roll <= need) continue
      if (luckSpentAfter) return null
      const cost = roll - need
      return {
        skill: ev.skill,
        roll,
        need,
        target: ev.target || need,
        level: ev.level,
        cost,
        affordable: currentLuck >= cost,
        pushable: !matchesAnyKeyword(ev.skill, COMBAT_SKILL_HINTS),
      }
    }
  }
  return null
}

export interface QuickSkill {
  name: string
  value: number
}

export interface QuickItem {
  name: string
  desc: string
  count: number
  equipped: boolean
}

export function quickCharacterSkills(card: CharacterCard | null | undefined, minVal = 30): QuickSkill[] {
  const raw = (card as { skills?: Record<string, unknown> } | null | undefined)?.skills
  if (!raw || typeof raw !== 'object') return []
  const list: QuickSkill[] = []
  for (const [name, val] of Object.entries(raw)) {
    const num = Number(val)
    if (Number.isFinite(num) && num >= minVal) list.push({ name, value: num })
  }
  list.sort((a, b) => b.value - a.value || a.name.localeCompare(b.name, 'zh-Hans-CN'))
  return list
}

export function quickCharacterItems(card: CharacterCard | null | undefined): QuickItem[] {
  const raw = (card as { inventory?: unknown } | null | undefined)?.inventory
  if (!Array.isArray(raw)) return []
  const list: QuickItem[] = []
  for (const entry of raw) {
    if (typeof entry === 'string' && entry.trim()) {
      list.push({ name: entry.trim(), desc: '', count: 1, equipped: false })
    } else if (entry && typeof entry === 'object') {
      const obj = entry as { name?: unknown; desc?: unknown; count?: unknown; equipped?: unknown }
      const name = String(obj.name ?? '').trim()
      if (!name) continue
      list.push({
        name,
        desc: String(obj.desc ?? '').trim(),
        count: Number(obj.count) || 1,
        equipped: Boolean(obj.equipped),
      })
    }
  }
  return list
}

// NOTE: 处于临时性或不定性疯狂（潜伏疯狂期）时允许玩家主动向 KP 请求现实检定。
export function isCharacterMad(card: CharacterCard | null | undefined): boolean {
  const state = String((card as { madness_state?: unknown } | null | undefined)?.madness_state || '')
  return state === 'temporary' || state === 'indefinite'
}

export function characterMadnessLabel(card: CharacterCard | null | undefined): string {
  const state = String((card as { madness_state?: unknown } | null | undefined)?.madness_state || '')
  if (state === 'temporary') return '临时性疯狂'
  if (state === 'indefinite') return '不定性疯狂'
  return ''
}

