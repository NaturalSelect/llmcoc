import type { CharacterCardFull, InventoryItem, InventoryItemType, RegenField } from '@/api/characterTypes'

type Costs = Record<string, number> | null | undefined

export const DEFAULT_REVIVE_COST = 2000
export const DEFAULT_REGEN_COST = 100
export const DEFAULT_PORTRAIT_COST = 100

export interface Entry {
  title: string
  lines: string[]
  // NOTE: 删除时传回后端的原始名称；缺失（未命名条目）时点击删除不做任何事，与旧前端一致
  key?: string
}

export function errMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

// NOTE: 费率为 0 是合法值（免费），所以只用 ?? 回落，不能用 ||
export function reviveCost(costs: Costs): number {
  return costs?.revive_base_cost ?? DEFAULT_REVIVE_COST
}

export function reviveConfirmMessage(cost: number, name: string | undefined): string {
  return `确认花费 ${cost} 金币复活「${name}」？\n复活后HP为最大值的一半，随机失去一半装备，遗忘一半法术。`
}

export function deleteDeadConfirmMessage(name: string | undefined): string {
  return `确认彻底删除「${name}」？此操作不可撤销，角色将永久消逝。`
}

export function cardSubtitle(c: CharacterCardFull): string {
  return [c.race || '人类', c.occupation || '未知职业', c.age ? `${c.age}岁` : '', c.gender || ''].filter(Boolean).join(' · ')
}

export function deadSubtitle(c: CharacterCardFull): string {
  return [c.race || '人类', c.occupation || '未知职业'].join(' · ')
}

export function detailSubtitle(c: CharacterCardFull): string {
  return [c.race || '人类', c.occupation, c.age ? `${c.age}岁` : '', c.gender].filter(Boolean).join(' · ')
}

const MADNESS_LABELS: Record<string, string> = { temporary: '临时疯狂', indefinite: '不定性疯狂', permanent: '永久疯狂' }
const MADNESS_HEADINGS: Record<string, string> = {
  temporary: '⚡ 临时疯狂',
  indefinite: '🌀 不定性疯狂',
  permanent: '☠ 永久疯狂',
}

export function hasMadness(c: CharacterCardFull): boolean {
  return !!c.madness_state && c.madness_state !== 'none'
}

export function madnessLabel(state: string | undefined): string {
  return (state && MADNESS_LABELS[state]) || state || ''
}

export function madnessHeading(state: string | undefined): string {
  return (state && MADNESS_HEADINGS[state]) || '疯狂状态'
}

// NOTE: 按技能值从高到低；值相同保持原有顺序（Array.sort 稳定）
export function sortedSkills(skills: Record<string, number> | null | undefined): Array<[string, number]> {
  return Object.entries(skills ?? {}).sort((a, b) => b[1] - a[1])
}

export const ITEM_TYPE_LABEL: Record<InventoryItemType, string> = {
  equipment: '装备',
  weapon: '武器',
  accessory: '配件',
  tome: '典籍',
  artifact: '神器',
  misc: '杂物',
  story: '剧情物品',
}

// NOTE: 剧情物品只能由 KP 在对局中发放，管理员手动添加和存入仓库都不接受
export const MANUAL_ITEM_TYPE_LABEL: Partial<Record<InventoryItemType, string>> = Object.fromEntries(
  Object.entries(ITEM_TYPE_LABEL).filter(([key]) => key !== 'story'),
)

// NOTE: 必须与后端 models.Item.Label() 完全一致：名称(描述, xN)，无描述且数量 1 只显示名称
export function itemLabel(it: InventoryItem): string {
  const parts: string[] = []
  if (it.desc) parts.push(it.desc)
  if (it.count > 1) parts.push(`x${it.count}`)
  return parts.length ? `${it.name}(${parts.join(', ')})` : it.name
}

// NOTE: 装备栏是物品栏里 equipped 为 true 的部分，两侧各自保持原有顺序
export function splitInventory(items: readonly InventoryItem[]): { equipped: InventoryItem[]; carried: InventoryItem[] } {
  const equipped: InventoryItem[] = []
  const carried: InventoryItem[] = []
  for (const it of items) (it.equipped === true ? equipped : carried).push(it)
  return { equipped, carried }
}

// NOTE: 仓库只允许操作启用且未进入房间的人物卡（后端对 in_session 的卡存取返回 409）
export function warehouseUsableCharacters(list: CharacterCardFull[]): CharacterCardFull[] {
  return list.filter((c) => c.is_active !== false && !c.in_session)
}

export function warehouseOptionLabel(c: CharacterCardFull): string {
  return (c.name ?? '') + (c.occupation ? `（${c.occupation}）` : '')
}

// NOTE: 背包/法术/社会关系/资产/仓库接口返回的整张卡里 in_session 是未计算的默认 false（运行时字段，只有列表接口会算），
// 直接替换会让「在房间中」的卡被误判为可存取仓库，所以沿用旧值
export function mergeUpdatedCard(prev: CharacterCardFull | null | undefined, updated: CharacterCardFull): CharacterCardFull {
  if (prev?.in_session === undefined) return updated
  return { ...updated, in_session: prev.in_session }
}

export interface RegenSpec {
  field: RegenField
  heading: string
  noun: string
  costKey: string
  placeholder: string
  doneToast: string
}

// NOTE: 顺序与旧详情弹窗一致：个人经历、外貌、性格
export const REGEN_SPECS: readonly RegenSpec[] = [
  {
    field: 'backstory',
    heading: '个人经历',
    noun: '个人经历',
    costKey: 'regenerate_backstory_cost',
    placeholder: '可选：补充个人经历指导，如「曾是一名水手」「家道中落」',
    doneToast: '个人经历已重新生成',
  },
  {
    field: 'appearance',
    heading: '外貌描述',
    noun: '外貌',
    costKey: 'regenerate_appearance_cost',
    placeholder: '可选：补充外貌指导，如「戴金丝眼镜」「左脸有一道疤痕」',
    doneToast: '外貌已重新生成',
  },
  {
    field: 'traits',
    heading: '性格特征',
    noun: '性格特征',
    costKey: 'regenerate_traits_cost',
    placeholder: '可选：补充性格特征指导，如「毒舌」「傲娇」',
    doneToast: '性格特征已重新生成',
  },
]

export function regenCost(costs: Costs, spec: RegenSpec): number {
  return costs?.[spec.costKey] ?? DEFAULT_REGEN_COST
}

export function regenConfirmMessage(spec: RegenSpec, cost: number, name: string | undefined): string {
  return `确认花费 ${cost} 金币为「${name}」重新生成${spec.noun}？`
}

export function regenTitle(spec: RegenSpec, cost: number, coins: number): string {
  return `花费 ${cost} 金币重新生成${spec.noun}（当前金币：${coins}）`
}

// NOTE: 图片接口按小写哈希存取，所以地址统一转小写；没有立绘返回空串，调用方用 || 回落到头像
export function portraitSrc(card: { portrait_hash?: string } | null | undefined): string {
  const hash = card?.portrait_hash
  return hash ? '/api/portraits/' + hash.toLowerCase() : ''
}

export function portraitCost(costs: Costs): number {
  return costs?.regenerate_portrait_cost ?? DEFAULT_PORTRAIT_COST
}

export function portraitConfirmMessage(cost: number, name: string | undefined, regenerate: boolean): string {
  return `确认花费 ${cost} 金币为「${name}」${regenerate ? '重新生成' : '生成'}立绘？生成可能需要 1 到 3 分钟，期间请勿关闭页面。`
}

export function portraitTitle(cost: number, coins: number, regenerate: boolean): string {
  return `花费 ${cost} 金币${regenerate ? '重新生成' : '生成'}立绘（当前金币：${coins}）`
}
