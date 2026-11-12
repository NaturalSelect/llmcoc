import { describe, expect, test } from 'vitest'
import {
  attitudeTone,
  clueNatureLabel,
  clueNatureTone,
  countLabel,
  entriesOf,
  hasMythosSkill,
  keeperAppendixRows,
  listLength,
  mechanicStageLabel,
  mechanicStageText,
  phaseLabel,
  playersLabel,
  scenarioContent,
  splitTags,
  startTimeLabel,
} from './scenarioFormat'

describe('startTimeLabel', () => {
  test('有值时转成年月日，月日去前导零', () => {
    expect(startTimeLabel('1924-09-03T21:30')).toBe('1924年9月3日 21:30')
    expect(startTimeLabel('1925-12-25T00:00')).toBe('1925年12月25日 00:00')
  })

  test('空字符串、null、undefined 显示未设置', () => {
    expect(startTimeLabel('')).toBe('未设置')
    expect(startTimeLabel(null)).toBe('未设置')
    expect(startTimeLabel(undefined)).toBe('未设置')
  })
})

describe('scenarioContent', () => {
  test('content 缺失或不是对象时返回空对象', () => {
    expect(scenarioContent(null)).toEqual({})
    expect(scenarioContent({ id: 1, name: 'a' })).toEqual({})
    expect(scenarioContent({ id: 1, name: 'a', content: 'oops' as unknown as Record<string, unknown> })).toEqual({})
  })

  test('对象原样返回', () => {
    const content = { setting: '雾' }
    expect(scenarioContent({ id: 1, name: 'a', content })).toBe(content)
  })
})

describe('标签与人数', () => {
  test('splitTags 去空白、去空项', () => {
    expect(splitTags(' 悬疑, 克苏鲁 ,,')).toEqual(['悬疑', '克苏鲁'])
    expect(splitTags('')).toEqual([])
    expect(splitTags(undefined)).toEqual([])
  })

  test('playersLabel 缺省为 1-4 人', () => {
    expect(playersLabel(2, 5)).toBe('2-5人')
    expect(playersLabel(undefined, undefined)).toBe('1-4人')
    expect(playersLabel(0, 0)).toBe('1-4人')
  })

  test('countLabel / listLength', () => {
    expect(countLabel(3, '个')).toBe('共 3 个')
    expect(listLength(null)).toBe(0)
    expect(listLength([1, 2])).toBe(2)
  })
})

describe('时间线、NPC、线索的标签与色调', () => {
  test('phaseLabel：past 是过去线，其余是当天推进', () => {
    expect(phaseLabel('past')).toBe('过去线')
    expect(phaseLabel('current')).toBe('当天推进')
  })

  test('attitudeTone', () => {
    expect(attitudeTone('hostile')).toBe('seal')
    expect(attitudeTone('friendly')).toBe('moss')
    expect(attitudeTone('neutral')).toBe('faint')
    expect(attitudeTone(undefined)).toBe('faint')
  })

  test('线索性质：缺省按真实处理', () => {
    expect(clueNatureLabel(undefined)).toBe('真实')
    expect(clueNatureLabel('隐藏')).toBe('隐藏')
    expect(clueNatureTone('隐藏')).toBe('seal')
    expect(clueNatureTone('误导')).toBe('faint')
    expect(clueNatureTone('真实')).toBe('moss')
    expect(clueNatureTone(undefined)).toBe('moss')
  })

  test('entriesOf 容忍 null', () => {
    expect(entriesOf(null)).toEqual([])
    expect(entriesOf({ STR: 50 })).toEqual([['STR', 50]])
  })

  test('hasMythosSkill：0 要显示，null/undefined 不显示', () => {
    expect(hasMythosSkill(0)).toBe(true)
    expect(hasMythosSkill(null)).toBe(false)
    expect(hasMythosSkill(undefined)).toBe(false)
  })
})

describe('keeperAppendixRows', () => {
  test('按固定顺序输出非空字段，长文标记 multiline', () => {
    const rows = keeperAppendixRows({ theme_guidance: '主题', core_truth: '真相', solo_advice: '', difficulty_up: '加难' })
    expect(rows.map((r) => r.label)).toEqual(['核心真相：', '提高难度：', '主题把握：'])
    expect(rows[0]).toMatchObject({ text: '真相', multiline: true })
    expect(rows[1]).toMatchObject({ text: '加难', multiline: false })
  })

  test('空附录或缺失返回空数组', () => {
    expect(keeperAppendixRows(null)).toEqual([])
    expect(keeperAppendixRows({})).toEqual([])
  })
})

describe('量化机制阶段', () => {
  test('标签带全角冒号；触发与效果用箭头连接，缺项时不留箭头', () => {
    expect(mechanicStageLabel({ label: '一阶' })).toBe('一阶：')
    expect(mechanicStageText({ trigger: '被发现', effect: '警报' })).toBe('被发现 → 警报')
    expect(mechanicStageText({ trigger: '被发现' })).toBe('被发现')
    expect(mechanicStageText({ effect: '警报' })).toBe('警报')
    expect(mechanicStageText({})).toBe('')
  })
})
