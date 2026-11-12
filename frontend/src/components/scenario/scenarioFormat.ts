import type { Scenario } from '@/api/scenarioTypes'
import type { ScenarioContentView, ScenarioKeeperAppendix, ScenarioMechanicStage } from '@/api/sessionTypes'
import { parseTimePoint } from '@/game/roomState'

export type Tone = 'seal' | 'ledger' | 'moss' | 'faint'

// NOTE: game_start_time 是 KP 自由书写的 YYYY-MM-DDTHH:MM，可能为空；展示格式与剧中时间点保持一致
export function startTimeLabel(v: string | null | undefined): string {
  return (v && parseTimePoint(v)?.label) || '未设置'
}

// NOTE: Scenario.content 在共享类型里是宽松 Record，渲染前统一收窄；content 不是对象时按空模组处理
export function scenarioContent(s: Scenario | null | undefined): ScenarioContentView {
  const c = s?.content
  return c && typeof c === 'object' ? (c as ScenarioContentView) : {}
}

export function splitTags(tags: string | null | undefined): string[] {
  return (tags || '')
    .split(',')
    .map((t) => t.trim())
    .filter(Boolean)
}

export function playersLabel(min?: number | null, max?: number | null): string {
  return `${min || 1}-${max || 4}人`
}

export function countLabel(n: number, unit: string): string {
  return `共 ${n} ${unit}`
}

export function listLength(list: readonly unknown[] | null | undefined): number {
  return list?.length ?? 0
}

export function phaseLabel(phase: string): string {
  return phase === 'past' ? '过去线' : '当天推进'
}

export function attitudeTone(attitude: string | undefined): Tone {
  return attitude === 'hostile' ? 'seal' : attitude === 'friendly' ? 'moss' : 'faint'
}

export function clueNatureLabel(nature: string | undefined): string {
  return nature || '真实'
}

export function clueNatureTone(nature: string | undefined): Tone {
  return nature === '隐藏' ? 'seal' : nature === '误导' ? 'faint' : 'moss'
}

export function entriesOf(rec: Record<string, number | string> | null | undefined): Array<[string, number | string]> {
  return Object.entries(rec ?? {})
}

// NOTE: 克苏鲁神话技能值 0 也要展示，只有缺省/null 才隐藏
export function hasMythosSkill(v: number | null | undefined): v is number {
  return v !== undefined && v !== null
}

export interface AppendixRow {
  key: keyof ScenarioKeeperAppendix
  label: string
  text: string
  // 核心真相、施动者设定是长文，保留换行
  multiline: boolean
}

const APPENDIX_FIELDS: Array<Pick<AppendixRow, 'key' | 'label' | 'multiline'>> = [
  { key: 'core_truth', label: '核心真相：', multiline: true },
  { key: 'antagonist_dossier', label: '施动者设定：', multiline: true },
  { key: 'difficulty_down', label: '降低难度：', multiline: false },
  { key: 'difficulty_up', label: '提高难度：', multiline: false },
  { key: 'solo_advice', label: '单人团：', multiline: false },
  { key: 'group_advice', label: '多人团：', multiline: false },
  { key: 'horror_tips', label: '恐怖呈现：', multiline: false },
  { key: 'theme_guidance', label: '主题把握：', multiline: false },
]

export function keeperAppendixRows(a: ScenarioKeeperAppendix | null | undefined): AppendixRow[] {
  if (!a) return []
  return APPENDIX_FIELDS.flatMap((f) => {
    const text = a[f.key]
    return text ? [{ ...f, text }] : []
  })
}

export function mechanicStageLabel(st: ScenarioMechanicStage): string {
  return (st.label ?? '') + '：'
}

export function mechanicStageText(st: ScenarioMechanicStage): string {
  return [st.trigger, st.effect].filter(Boolean).join(' → ')
}
