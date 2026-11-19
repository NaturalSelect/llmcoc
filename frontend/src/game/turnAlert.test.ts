// @vitest-environment jsdom
import { beforeEach, describe, expect, test } from 'vitest'
import { formatTurnTitle, loadTurnSoundPref, playTurnChime, saveTurnSoundPref } from './turnAlert'

describe('turnAlert', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  test('默认开启提示音，可持久化关闭与重新开启', () => {
    expect(loadTurnSoundPref()).toBe(true)
    saveTurnSoundPref(false)
    expect(loadTurnSoundPref()).toBe(false)
    saveTurnSoundPref(true)
    expect(loadTurnSoundPref()).toBe(true)
  })

  test('标题在被点名时加前缀，不重复叠加，取消后还原', () => {
    expect(formatTurnTitle('雾港旧宅 · LLM-COC', true)).toBe('【轮到你了】雾港旧宅 · LLM-COC')
    expect(formatTurnTitle('【轮到你了】雾港旧宅 · LLM-COC', true)).toBe('【轮到你了】雾港旧宅 · LLM-COC')
    expect(formatTurnTitle('【轮到你了】雾港旧宅 · LLM-COC', false)).toBe('雾港旧宅 · LLM-COC')
  })

  test('无 AudioContext 时调用 playTurnChime 不会抛错', () => {
    expect(() => playTurnChime()).not.toThrow()
  })
})
