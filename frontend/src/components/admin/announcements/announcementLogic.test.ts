import { describe, expect, test } from 'vitest'
import type { Announcement } from '@/api/adminTypesContent'
import {
  announcementStatusLabel,
  emptyAnnouncementForm,
  formFromAnnouncement,
  levelLabel,
  toAnnouncementPayload,
  toDatetimeLocalValue,
} from './announcementLogic'

const NOW = new Date('2026-06-15T12:00:00Z')

describe('announcementStatusLabel', () => {
  const base = { is_active: true, starts_at: null, ends_at: null }

  test('已下线优先于时间窗口', () => {
    expect(announcementStatusLabel({ ...base, is_active: false, ends_at: '2020-01-01T00:00:00Z' }, NOW)).toBe('已下线')
  })

  test('未开始、已过期、展示中', () => {
    expect(announcementStatusLabel({ ...base, starts_at: '2026-06-16T00:00:00Z' }, NOW)).toBe('未开始')
    expect(announcementStatusLabel({ ...base, ends_at: '2026-06-15T11:59:59Z' }, NOW)).toBe('已过期')
    expect(announcementStatusLabel({ ...base, starts_at: '2026-06-01T00:00:00Z', ends_at: '2026-07-01T00:00:00Z' }, NOW)).toBe('展示中')
    expect(announcementStatusLabel(base, NOW)).toBe('展示中')
  })

  test('结束时间恰好等于当前时刻算已过期，开始时间恰好等于当前时刻算展示中', () => {
    expect(announcementStatusLabel({ ...base, ends_at: NOW.toISOString() }, NOW)).toBe('已过期')
    expect(announcementStatusLabel({ ...base, starts_at: NOW.toISOString() }, NOW)).toBe('展示中')
  })
})

describe('levelLabel', () => {
  test('三个等级映射中文，未知等级原样返回', () => {
    expect(levelLabel('normal')).toBe('普通')
    expect(levelLabel('important')).toBe('重要')
    expect(levelLabel('warning')).toBe('警告')
    expect(levelLabel('legacy')).toBe('legacy')
  })
})

describe('datetime-local 往返', () => {
  test('toDatetimeLocalValue 输出本地时间，空值得到空串', () => {
    const local = new Date(2026, 0, 2, 3, 4)
    expect(toDatetimeLocalValue(local.toISOString())).toBe('2026-01-02T03:04')
    expect(toDatetimeLocalValue(null)).toBe('')
    expect(toDatetimeLocalValue('')).toBe('')
  })

  test('回填后再提交得到同一时刻（精度到分钟）', () => {
    const iso = new Date(2026, 5, 15, 8, 30).toISOString()
    const form = { ...emptyAnnouncementForm(), title: 't', starts_at: toDatetimeLocalValue(iso) }
    expect(toAnnouncementPayload(form).starts_at).toBe(iso)
  })
})

describe('toAnnouncementPayload', () => {
  test('留空的时间提交 null，其余字段原样透传', () => {
    const form = { ...emptyAnnouncementForm(), title: ' 标题 ', content: '正文', level: 'warning', is_pinned: true, is_active: false }
    expect(toAnnouncementPayload(form)).toStrictEqual({
      title: ' 标题 ',
      content: '正文',
      level: 'warning',
      is_pinned: true,
      is_active: false,
      starts_at: null,
      ends_at: null,
    })
  })
})

describe('formFromAnnouncement', () => {
  test('编辑回填：时间转为本地输入格式，null 时间变空串', () => {
    const a: Announcement = {
      id: 1,
      title: 'T',
      content: 'C',
      level: 'important',
      is_pinned: true,
      is_active: true,
      starts_at: new Date(2026, 0, 2, 3, 4).toISOString(),
      ends_at: null,
      created_at: '2026-01-01T00:00:00Z',
    }
    expect(formFromAnnouncement(a)).toStrictEqual({
      title: 'T',
      content: 'C',
      level: 'important',
      is_pinned: true,
      is_active: true,
      starts_at: '2026-01-02T03:04',
      ends_at: '',
    })
  })
})
