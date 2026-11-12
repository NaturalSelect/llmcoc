// @vitest-environment node
import { describe, expect, test } from 'vitest'
import type { CharacterCardFull, InventoryItem } from '@/api/characterTypes'
import {
  ITEM_TYPE_LABEL,
  MANUAL_ITEM_TYPE_LABEL,
  REGEN_SPECS,
  cardSubtitle,
  deadSubtitle,
  deleteDeadConfirmMessage,
  detailSubtitle,
  hasMadness,
  itemLabel,
  madnessHeading,
  madnessLabel,
  mergeUpdatedCard,
  portraitConfirmMessage,
  portraitCost,
  portraitSrc,
  portraitTitle,
  regenConfirmMessage,
  regenCost,
  regenTitle,
  reviveConfirmMessage,
  reviveCost,
  sortedSkills,
  splitInventory,
  warehouseOptionLabel,
  warehouseUsableCharacters,
} from './characterLogic'

const card = (over: Partial<CharacterCardFull> = {}): CharacterCardFull => ({ id: 1, name: '艾米', ...over })

describe('费率与确认文案', () => {
  test('复活费用缺省 2000，0 是合法值', () => {
    expect(reviveCost({})).toBe(2000)
    expect(reviveCost(undefined)).toBe(2000)
    expect(reviveCost({ revive_base_cost: 0 })).toBe(0)
    expect(reviveCost({ revive_base_cost: 3000 })).toBe(3000)
  })

  test('复活与彻底删除文案与旧前端一致', () => {
    expect(reviveConfirmMessage(2000, '艾米')).toBe('确认花费 2000 金币复活「艾米」？\n复活后HP为最大值的一半，随机失去一半装备，遗忘一半法术。')
    expect(deleteDeadConfirmMessage('艾米')).toBe('确认彻底删除「艾米」？此操作不可撤销，角色将永久消逝。')
  })

  test('重新生成：三个字段的费率键、缺省值与文案', () => {
    expect(REGEN_SPECS.map((s) => s.field)).toEqual(['backstory', 'appearance', 'traits'])
    const [backstory, appearance, traits] = REGEN_SPECS as [(typeof REGEN_SPECS)[0], (typeof REGEN_SPECS)[0], (typeof REGEN_SPECS)[0]]
    expect(regenCost({}, appearance)).toBe(100)
    expect(regenCost({ regenerate_traits_cost: 0 }, traits)).toBe(0)
    expect(regenCost({ regenerate_backstory_cost: 150 }, backstory)).toBe(150)
    expect(regenConfirmMessage(appearance, 100, '艾米')).toBe('确认花费 100 金币为「艾米」重新生成外貌？')
    expect(regenConfirmMessage(backstory, 150, '艾米')).toBe('确认花费 150 金币为「艾米」重新生成个人经历？')
    expect(regenConfirmMessage(traits, 100, '艾米')).toBe('确认花费 100 金币为「艾米」重新生成性格特征？')
    expect(regenTitle(traits, 100, 42)).toBe('花费 100 金币重新生成性格特征（当前金币：42）')
    expect(appearance.doneToast).toBe('外貌已重新生成')
    expect(backstory.doneToast).toBe('个人经历已重新生成')
    expect(traits.doneToast).toBe('性格特征已重新生成')
  })
})

describe('立绘', () => {
  test('portraitSrc：空值与缺失返回空串，哈希转小写拼成图片地址', () => {
    expect(portraitSrc(null)).toBe('')
    expect(portraitSrc(undefined)).toBe('')
    expect(portraitSrc(card())).toBe('')
    expect(portraitSrc(card({ portrait_hash: '' }))).toBe('')
    expect(portraitSrc(card({ portrait_hash: 'ab12cd' }))).toBe('/api/portraits/ab12cd')
    expect(portraitSrc(card({ portrait_hash: 'AB12CD' }))).toBe('/api/portraits/ab12cd')
  })

  test('portraitCost：缺省 100，0 是合法值', () => {
    expect(portraitCost({})).toBe(100)
    expect(portraitCost(null)).toBe(100)
    expect(portraitCost(undefined)).toBe(100)
    expect(portraitCost({ regenerate_portrait_cost: 0 })).toBe(0)
    expect(portraitCost({ regenerate_portrait_cost: 250 })).toBe(250)
  })

  test('确认与按钮提示文案区分首次生成和重新生成', () => {
    expect(portraitConfirmMessage(100, '艾米', false)).toContain('确认花费 100 金币为「艾米」生成立绘？')
    expect(portraitConfirmMessage(100, '艾米', true)).toContain('确认花费 100 金币为「艾米」重新生成立绘？')
    expect(portraitTitle(100, 42, false)).toBe('花费 100 金币生成立绘（当前金币：42）')
    expect(portraitTitle(100, 42, true)).toBe('花费 100 金币重新生成立绘（当前金币：42）')
  })
})

describe('人物卡展示文案', () => {
  test('卡片副标题：缺省种族/职业，年龄带「岁」，空值省略', () => {
    expect(cardSubtitle(card({ occupation: '记者', age: 28, gender: '女' }))).toBe('人类 · 记者 · 28岁 · 女')
    expect(cardSubtitle(card())).toBe('人类 · 未知职业')
    expect(cardSubtitle(card({ race: '深潜者', occupation: '医生' }))).toBe('深潜者 · 医生')
  })

  test('阵亡卡与详情副标题', () => {
    expect(deadSubtitle(card({ occupation: '医生', age: 30 }))).toBe('人类 · 医生')
    expect(deadSubtitle(card())).toBe('人类 · 未知职业')
    expect(detailSubtitle(card())).toBe('人类')
    expect(detailSubtitle(card({ occupation: '医生', age: 30, gender: '男' }))).toBe('人类 · 医生 · 30岁 · 男')
  })

  test('疯狂状态：none 与空值视为无疯狂，未知状态原样显示', () => {
    expect(hasMadness(card())).toBe(false)
    expect(hasMadness(card({ madness_state: 'none' }))).toBe(false)
    expect(hasMadness(card({ madness_state: 'temporary' }))).toBe(true)
    expect(madnessLabel('indefinite')).toBe('不定性疯狂')
    expect(madnessLabel('weird')).toBe('weird')
    expect(madnessHeading('permanent')).toBe('☠ 永久疯狂')
    expect(madnessHeading('temporary')).toBe('⚡ 临时疯狂')
    expect(madnessHeading('weird')).toBe('疯狂状态')
    expect(madnessHeading(undefined)).toBe('疯狂状态')
  })

  test('技能按数值从高到低，同值保持原序，null 安全', () => {
    expect(sortedSkills({ 侦查: 25, 聆听: 20, 斗殴: 25 })).toEqual([
      ['侦查', 25],
      ['斗殴', 25],
      ['聆听', 20],
    ])
    expect(sortedSkills(null)).toEqual([])
    expect(sortedSkills(undefined)).toEqual([])
  })
})

describe('装备栏', () => {
  const item = (name: string, equipped?: boolean): InventoryItem => ({ name, count: 1, type: 'misc', ...(equipped === undefined ? {} : { equipped }) })

  test('空列表得到两个空数组', () => {
    expect(splitInventory([])).toEqual({ equipped: [], carried: [] })
  })

  test('全部装备或全部未装备', () => {
    const all = [item('甲', true), item('乙', true)]
    expect(splitInventory(all)).toEqual({ equipped: all, carried: [] })
    const none = [item('甲'), item('乙', false)]
    expect(splitInventory(none)).toEqual({ equipped: [], carried: none })
  })

  test('混合时两侧各自保持原有顺序，返回的是原对象', () => {
    const a = item('甲')
    const b = item('乙', true)
    const c = item('丙', false)
    const d = item('丁', true)
    const e = item('戊')
    const r = splitInventory([a, b, c, d, e])
    expect(r.equipped.map((i) => i.name)).toEqual(['乙', '丁'])
    expect(r.carried.map((i) => i.name)).toEqual(['甲', '丙', '戊'])
    expect(r.equipped[0]).toBe(b)
    expect(r.carried[2]).toBe(e)
  })

  test('itemLabel 不显示装备状态', () => {
    expect(itemLabel({ name: '手电筒', desc: '黄铜', count: 1, type: 'equipment', equipped: true })).toBe('手电筒(黄铜)')
  })
})

describe('仓库', () => {
  test('itemLabel 与后端 Label() 四种形态一致', () => {
    expect(itemLabel({ name: '绷带', count: 1, type: 'misc' })).toBe('绷带')
    expect(itemLabel({ name: '绷带', count: 3, type: 'misc' })).toBe('绷带(x3)')
    expect(itemLabel({ name: '绷带', desc: '染血', count: 1, type: 'misc' })).toBe('绷带(染血)')
    expect(itemLabel({ name: '绷带', desc: '染血', count: 3, type: 'misc' })).toBe('绷带(染血, x3)')
    expect(itemLabel({ name: '绷带', desc: '', count: 1, type: 'misc' })).toBe('绷带')
  })

  test('物品类型中文名覆盖全部类型', () => {
    expect(ITEM_TYPE_LABEL).toEqual({ equipment: '装备', weapon: '武器', accessory: '配件', tome: '典籍', artifact: '神器', misc: '杂物', story: '剧情物品' })
    expect(Object.keys(MANUAL_ITEM_TYPE_LABEL)).toEqual(['equipment', 'weapon', 'accessory', 'tome', 'artifact', 'misc'])
  })
  test('只允许启用且不在房间中的人物卡取出', () => {
    const list = [
      card({ id: 1, is_active: true }),
      card({ id: 2, is_active: false }),
      card({ id: 3, in_session: true }),
      card({ id: 4 }),
    ]
    expect(warehouseUsableCharacters(list).map((c) => c.id)).toEqual([1, 4])
    expect(warehouseUsableCharacters([])).toEqual([])
  })

  test('下拉选项文案：有职业时带全角括号', () => {
    expect(warehouseOptionLabel(card({ occupation: '医生' }))).toBe('艾米（医生）')
    expect(warehouseOptionLabel(card())).toBe('艾米')
  })

  test('服务端返回的整张卡保留列表里已知的 in_session', () => {
    const updated = card({ in_session: false, inventory: [{ name: '绷带', count: 1, type: 'misc' }] })
    expect(mergeUpdatedCard(card({ in_session: true }), updated)).toEqual({ ...updated, in_session: true })
    expect(mergeUpdatedCard(card(), updated)).toBe(updated)
    expect(mergeUpdatedCard(null, updated)).toBe(updated)
  })
})
