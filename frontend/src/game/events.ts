// NOTE: KP 工具结算事件(event 消息)的收窄与文案，全部是纯函数；图标和颜色只给字符串键，由组件映射
import { parseTimePoint } from './roomState'
import type { RoomMessage } from './roomState'

export interface GameEvent {
  type: string
  character?: string
  skill?: string
  roll?: number
  expr?: string
  level?: string
  target?: number
  result?: string
  hidden?: boolean
  field?: string
  from?: number
  to?: number
  state?: string
  category?: string
  op?: string
  name?: string
  detail?: string
  symptom?: string
  duration?: number
  location?: string
  time?: string
  round?: number
  amount?: number
}

export type EventIcon =
  | 'dice'
  | 'hidden'
  | 'hp'
  | 'san'
  | 'mp'
  | 'stat'
  | 'armor'
  | 'wound'
  | 'item'
  | 'spell'
  | 'monster'
  | 'relation'
  | 'asset'
  | 'madness'
  | 'location'
  | 'time'
  | 'combat'
  | 'chase'
  | 'coins'
  | 'backstory'

export type EventTone = 'plain' | 'hp' | 'san' | 'mp' | 'danger'

// text 是普通文字，value 是需要强调的数值(点数、变化后的值)，可以为空
export interface EventView {
  icon: EventIcon
  tone: EventTone
  text: string
  value?: string
  note?: string
}

const STR_KEYS = ['type', 'character', 'skill', 'expr', 'level', 'result', 'field', 'state', 'category', 'op', 'name', 'detail', 'symptom', 'location', 'time'] as const
const NUM_KEYS = ['roll', 'target', 'from', 'to', 'duration', 'round', 'amount'] as const

// NOTE: meta.events 来自服务器 JSON，逐字段校验类型，丢掉不合法的条目
export function eventsOf(msg: RoomMessage | null | undefined): GameEvent[] {
  const raw = (msg?.meta as { events?: unknown } | null | undefined)?.events
  if (!Array.isArray(raw)) return []
  const out: GameEvent[] = []
  for (const item of raw) {
    if (!item || typeof item !== 'object') continue
    const src = item as Record<string, unknown>
    if (typeof src.type !== 'string' || !src.type) continue
    const ev: Record<string, unknown> = {}
    for (const k of STR_KEYS) if (typeof src[k] === 'string') ev[k] = src[k]
    for (const k of NUM_KEYS) if (typeof src[k] === 'number' && Number.isFinite(src[k])) ev[k] = src[k]
    if (src.hidden === true) ev.hidden = true
    out.push(ev as unknown as GameEvent)
  }
  return out
}

const FIELD_LABEL: Record<string, string> = {
  hp: 'HP',
  max_hp: 'HP上限',
  san: 'SAN',
  max_san: 'SAN上限',
  mp: 'MP',
  max_mp: 'MP上限',
  pow: '意志',
  cthulhu_mythos: '克苏鲁神话',
  armor: '护甲',
  str: '力量',
  con: '体质',
  siz: '体型',
  dex: '敏捷',
  app: '外貌',
  int: '智力',
  edu: '教育',
}

const WOUND_LABEL: Record<string, string> = {
  none: '伤势恢复',
  major: '受到重伤',
  dying: '濒死',
  dead: '死亡',
  unconscious: '陷入昏迷',
  conscious: '恢复意识',
}

const MADNESS_LABEL: Record<string, string> = {
  temporary: '临时性疯狂',
  indefinite: '不定性疯狂',
  permanent: '永久性疯狂',
}

const ITEM_ICON: Record<string, EventIcon> = {
  inventory: 'item',
  spell: 'spell',
  monster: 'monster',
  relation: 'relation',
  asset: 'asset',
}

const ITEM_VERB: Record<string, Record<string, string>> = {
  inventory: { add: '获得', remove: '失去', change: '物品变化', equip: '装备', unequip: '卸下' },
  spell: { add: '学会法术', remove: '遗失法术', change: '法术变化' },
  monster: { add: '记录神话存在', remove: '移除神话存在记录', change: '神话存在记录变化' },
  relation: { add: '记录关系', remove: '移除关系', change: '关系变化' },
  asset: { add: '获得资产', remove: '失去资产', change: '资产变化' },
}

function who(ev: GameEvent): string {
  return ev.character ? ev.character + ' ' : ''
}

function statTone(field: string | undefined): EventTone {
  if (field === 'hp' || field === 'max_hp') return 'hp'
  if (field === 'san' || field === 'max_san') return 'san'
  if (field === 'mp' || field === 'max_mp') return 'mp'
  return 'plain'
}

function statIcon(field: string | undefined): EventIcon {
  if (field === 'hp' || field === 'max_hp') return 'hp'
  if (field === 'san' || field === 'max_san') return 'san'
  if (field === 'mp' || field === 'max_mp') return 'mp'
  if (field === 'armor') return 'armor'
  return 'stat'
}

function encounterView(ev: GameEvent, icon: 'combat' | 'chase', noun: string): EventView {
  if (ev.op === 'start') return { icon, tone: 'plain', text: noun + '开始' }
  if (ev.op === 'end') return { icon, tone: 'plain', text: noun + '结束' }
  return { icon, tone: 'plain', text: noun + `进入第 ${ev.round ?? '?'} 轮` }
}

export function describeEvent(ev: GameEvent): EventView {
  switch (ev.type) {
    case 'dice': {
      if (ev.hidden) return { icon: 'hidden', tone: 'plain', text: 'KP 暗骰了一次' }
      // NOTE: 有目标值时写成「技能 目标 N · 骰值 · 结论」；查不到目标值(NPC、伤害骰)时只显示骰值和难度
      const hasTarget = typeof ev.target === 'number' && ev.target > 0
      const notes: string[] = []
      if (hasTarget) notes.push('目标 ' + ev.target)
      if (ev.level) notes.push('难度 ' + ev.level)
      if (hasTarget && ev.result) notes.push(ev.result)
      if (ev.detail) notes.push(ev.detail)
      return {
        icon: 'dice',
        tone: 'plain',
        text: who(ev) + (ev.skill || (ev.expr ? `骰点 ${ev.expr}` : '骰点')) + (hasTarget ? ' 骰值' : ''),
        value: String(ev.roll ?? ''),
        note: notes.length ? notes.join(' · ') : undefined,
      }
    }
    case 'stat': {
      const label = ev.field === 'skill' ? ev.skill || '技能' : FIELD_LABEL[ev.field || ''] || ev.field || '属性'
      return {
        icon: statIcon(ev.field),
        tone: statTone(ev.field),
        text: `${who(ev)}${label} ${ev.from ?? '?'} →`,
        value: String(ev.to ?? '?'),
        note: ev.detail || undefined,
      }
    }
    case 'wound': {
      const state = ev.state || 'none'
      const danger = state === 'major' || state === 'dying' || state === 'dead' || state === 'unconscious'
      return { icon: 'wound', tone: danger ? 'danger' : 'plain', text: who(ev) + (WOUND_LABEL[state] || state) }
    }
    case 'item': {
      const cat = ev.category || 'inventory'
      const verb = ITEM_VERB[cat]?.[ev.op || 'add'] || '变化'
      return { icon: ITEM_ICON[cat] || 'item', tone: 'plain', text: `${who(ev)}${verb}：${ev.name || ''}`, note: ev.detail }
    }
    case 'madness': {
      if (!ev.state || ev.state === 'none') return { icon: 'madness', tone: 'plain', text: who(ev) + '疯狂解除' }
      const label = MADNESS_LABEL[ev.state] || '疯狂'
      const duration = ev.duration ? `（持续 ${ev.duration}）` : ''
      return { icon: 'madness', tone: 'san', text: `${who(ev)}${label}`, note: (ev.symptom || '') + duration || undefined }
    }
    case 'location':
      return { icon: 'location', tone: 'plain', text: `${who(ev)}前往 ${ev.location || '未知地点'}` }
    case 'time':
      return { icon: 'time', tone: 'plain', text: '时间来到 ' + (parseTimePoint(ev.time)?.label || ev.time || '') }
    case 'combat':
      return encounterView(ev, 'combat', '战斗')
    case 'chase':
      return encounterView(ev, 'chase', '追逐')
    case 'coins':
      return { icon: 'coins', tone: 'plain', text: who(ev) + '获得金币', value: '+' + (ev.amount ?? 0), note: ev.detail || undefined }
    case 'backstory':
      return { icon: 'backstory', tone: 'plain', text: who(ev) + '个人经历有了新的篇章', note: ev.detail }
  }
  return { icon: 'stat', tone: 'plain', text: ev.type }
}

// 一条 event 消息里最后一次时间推进，没有时返回 ''
export function lastEventTime(msg: RoomMessage | null | undefined): string {
  const events = eventsOf(msg)
  for (let i = events.length - 1; i >= 0; i--) {
    const ev = events[i]
    if (ev?.type === 'time' && ev.time) return ev.time
  }
  return ''
}
