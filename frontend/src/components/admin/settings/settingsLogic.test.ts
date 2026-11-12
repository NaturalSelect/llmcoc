import { describe, expect, test } from 'vitest'
import {
  balanceRulesLength,
  COST_FIELDS,
  costValueToSubmit,
  DEFAULT_SITE_SETTINGS,
  parseSiteSettings,
  similarityValueToSubmit,
} from './settingsLogic'

describe('parseSiteSettings', () => {
  test('空表返回全部默认值', () => {
    expect(parseSiteSettings([])).toStrictEqual(DEFAULT_SITE_SETTINGS)
    expect(parseSiteSettings(null)).toStrictEqual(DEFAULT_SITE_SETTINGS)
  })

  test('开关：邀请码仅 "true" 为开；NSFW 仅 "false" 为关', () => {
    expect(parseSiteSettings([{ key: 'require_invite_code', value: 'true' }]).require_invite_code).toBe(true)
    expect(parseSiteSettings([{ key: 'require_invite_code', value: 'yes' }]).require_invite_code).toBe(false)
    expect(parseSiteSettings([{ key: 'allow_nsfw', value: 'false' }]).allow_nsfw).toBe(false)
    expect(parseSiteSettings([{ key: 'allow_nsfw', value: 'true' }]).allow_nsfw).toBe(true)
    expect(parseSiteSettings([{ key: 'allow_nsfw', value: '' }]).allow_nsfw).toBe(true)
  })

  test('数值：正常解析；无法解析回落默认；库里存 0 保留 0', () => {
    const s = parseSiteSettings([
      { key: 'initial_coins', value: '0' },
      { key: 'initial_card_slots', value: '8' },
      { key: 'revive_base_cost', value: 'abc' },
      { key: 'end_session_cost', value: '' },
    ])
    expect(s.initial_coins).toBe(0)
    expect(s.initial_card_slots).toBe(8)
    expect(s.revive_base_cost).toBe(2000)
    expect(s.end_session_cost).toBe(200)
  })

  test('session_retention_days：缺失默认 30，存 0 保留 0，无法解析回落默认', () => {
    const parse = (value: string) => parseSiteSettings([{ key: 'session_retention_days', value }]).session_retention_days
    expect(parseSiteSettings([]).session_retention_days).toBe(30)
    expect(parse('0')).toBe(0)
    expect(parse('7')).toBe(7)
    expect(parse('abc')).toBe(30)
  })

  test('balance_rules 缺失为空串，存在则原样保留', () => {
    expect(parseSiteSettings([]).balance_rules).toBe('')
    expect(parseSiteSettings([{ key: 'balance_rules', value: '规则一\n规则二' }]).balance_rules).toBe('规则一\n规则二')
  })
  test('lawyer_cache_min_similarity：缺失默认 0.3；合法值含负数与边界按浮点解析', () => {
    const parse = (value: string) => parseSiteSettings([{ key: 'lawyer_cache_min_similarity', value }]).lawyer_cache_min_similarity
    expect(parseSiteSettings([]).lawyer_cache_min_similarity).toBe(0.3)
    expect(parse('0.55')).toBe(0.55)
    expect(parse('0')).toBe(0)
    expect(parse('-1')).toBe(-1)
    expect(parse('1')).toBe(1)
    expect(parse('-0.25')).toBe(-0.25)
  })

  test('lawyer_cache_min_similarity：无法解析或越界回落默认值', () => {
    const parse = (value: string) => parseSiteSettings([{ key: 'lawyer_cache_min_similarity', value }]).lawyer_cache_min_similarity
    expect(parse('')).toBe(0.3)
    expect(parse('abc')).toBe(0.3)
    expect(parse('NaN')).toBe(0.3)
    expect(parse('1.5')).toBe(0.3)
    expect(parse('-1.01')).toBe(0.3)
    expect(parse('Infinity')).toBe(0.3)
  })
})

describe('costValueToSubmit', () => {
  test('合法值原样返回字符串', () => {
    expect(costValueToSubmit(100)).toBe('100')
    expect(costValueToSubmit('0')).toBe('0')
    expect(costValueToSubmit(1.5)).toBe('1.5')
  })

  test('空、无法解析、负数、null 都拒绝', () => {
    expect(costValueToSubmit('')).toBeNull()
    expect(costValueToSubmit(null)).toBeNull()
    expect(costValueToSubmit(undefined)).toBeNull()
    expect(costValueToSubmit('abc')).toBeNull()
    expect(costValueToSubmit(-1)).toBeNull()
  })
})

describe('similarityValueToSubmit', () => {
  test('范围内的数字（含边界与负数）返回规范化字符串', () => {
    expect(similarityValueToSubmit(0.3)).toBe('0.3')
    expect(similarityValueToSubmit('0.45')).toBe('0.45')
    expect(similarityValueToSubmit(' 0.5 ')).toBe('0.5')
    expect(similarityValueToSubmit(-1)).toBe('-1')
    expect(similarityValueToSubmit(1)).toBe('1')
    expect(similarityValueToSubmit('0')).toBe('0')
  })

  test('空、非数字、夹杂字符、越界都拒绝', () => {
    expect(similarityValueToSubmit('')).toBeNull()
    expect(similarityValueToSubmit('   ')).toBeNull()
    expect(similarityValueToSubmit(null)).toBeNull()
    expect(similarityValueToSubmit(undefined)).toBeNull()
    expect(similarityValueToSubmit('abc')).toBeNull()
    expect(similarityValueToSubmit('0.5abc')).toBeNull()
    expect(similarityValueToSubmit(1.01)).toBeNull()
    expect(similarityValueToSubmit(-1.01)).toBeNull()
    expect(similarityValueToSubmit(Infinity)).toBeNull()
    expect(similarityValueToSubmit(NaN)).toBeNull()
  })
})

describe('balanceRulesLength', () => {
  test('按 Unicode 字符计数，代理对算一个', () => {
    expect(balanceRulesLength('')).toBe(0)
    expect(balanceRulesLength(null)).toBe(0)
    expect(balanceRulesLength('规则')).toBe(2)
    expect(balanceRulesLength('😀😀')).toBe(2)
    expect('😀😀'.length).toBe(4)
  })
})

describe('COST_FIELDS', () => {
  test('十个数值项，键与后端站点设置一致', () => {
    expect(COST_FIELDS.map((f) => f.key)).toStrictEqual([
      'initial_coins',
      'initial_card_slots',
      'regenerate_appearance_cost',
      'regenerate_backstory_cost',
      'regenerate_traits_cost',
      'regenerate_portrait_cost',
      'revive_base_cost',
      'max_character_drafts',
      'end_session_cost',
      'session_retention_days',
    ])
  })
})
