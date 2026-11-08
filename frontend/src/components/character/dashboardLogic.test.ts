// @vitest-environment node
import { describe, expect, test } from 'vitest'
import type { Announcement } from '@/api/characterTypes'
import { announcementAccent, announcementToggleLabel, playersLabel, sessionStatusLabel, sessionStatusTone, visibleAnnouncements } from './dashboardLogic'

const ann = (id: number): Announcement => ({ id, title: `公告${id}`, content: '', level: 'normal', is_pinned: false })

describe('dashboardLogic', () => {
  test('房间状态标签与色调：非 lobby/playing 一律视为已结束', () => {
    expect(sessionStatusLabel('lobby')).toBe('大厅')
    expect(sessionStatusLabel('playing')).toBe('游戏中')
    expect(sessionStatusLabel('ended')).toBe('已结束')
    expect(sessionStatusLabel(undefined)).toBe('已结束')
    expect(sessionStatusTone('lobby')).toBe('ledger')
    expect(sessionStatusTone('playing')).toBe('moss')
    expect(sessionStatusTone('ended')).toBe('faint')
  })

  test('玩家人数文案', () => {
    expect(playersLabel({ id: 1, max_players: 4, players: [{}, {}] })).toBe('2/4 位调查员')
    expect(playersLabel({ id: 1, max_players: 4, players: null })).toBe('0/4 位调查员')
  })

  test('公告折叠：默认只显示前 3 条', () => {
    const list = [1, 2, 3, 4, 5].map(ann)
    expect(visibleAnnouncements(list, false).map((a) => a.id)).toEqual([1, 2, 3])
    expect(visibleAnnouncements(list, true)).toHaveLength(5)
    expect(announcementToggleLabel(false, 5)).toBe('展开全部（5）')
    expect(announcementToggleLabel(true, 5)).toBe('收起')
  })

  test('公告强调色：未知等级当普通公告', () => {
    expect(announcementAccent('important')).not.toBe(announcementAccent('normal'))
    expect(announcementAccent('warning')).not.toBe(announcementAccent('important'))
    expect(announcementAccent('whatever')).toBe(announcementAccent('normal'))
    expect(announcementAccent(undefined)).toBe(announcementAccent('normal'))
  })
})
