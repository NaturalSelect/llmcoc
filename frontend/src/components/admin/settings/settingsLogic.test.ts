import { describe, expect, test } from 'vitest'
import {
  balanceRulesLength,
  COST_FIELDS,
  costValueToSubmit,
  DEFAULT_SITE_SETTINGS,
  parseSiteSettings,
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

  test('balance_rules 缺失为空串，存在则原样保留', () => {
    expect(parseSiteSettings([]).balance_rules).toBe('')
    expect(parseSiteSettings([{ key: 'balance_rules', value: '规则一\n规则二' }]).balance_rules).toBe('规则一\n规则二')
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
  test('八个数值项，键与旧页面一致', () => {
    expect(COST_FIELDS.map((f) => f.key)).toStrictEqual([
      'initial_coins',
      'initial_card_slots',
      'regenerate_appearance_cost',
      'regenerate_backstory_cost',
      'regenerate_traits_cost',
      'revive_base_cost',
      'max_character_drafts',
      'end_session_cost',
    ])
  })
})
