import type { Announcement, AnnouncementForm, AnnouncementPayload } from '@/api/adminTypesContent'

export const LEVEL_OPTIONS = [
  { value: 'normal', label: '普通' },
  { value: 'important', label: '重要' },
  { value: 'warning', label: '警告' },
] as const

export function levelLabel(level: string): string {
  return LEVEL_OPTIONS.find((o) => o.value === level)?.label || level
}

export function emptyAnnouncementForm(): AnnouncementForm {
  return { title: '', content: '', level: 'normal', is_pinned: false, is_active: true, starts_at: '', ends_at: '' }
}

// NOTE: <input type="datetime-local"> 只认本地时间的 YYYY-MM-DDTHH:mm，后端返回的 ISO 字符串需要转换后才能回填编辑表单
export function toDatetimeLocalValue(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function formFromAnnouncement(a: Announcement): AnnouncementForm {
  return {
    title: a.title,
    content: a.content,
    level: a.level,
    is_pinned: a.is_pinned,
    is_active: a.is_active,
    starts_at: toDatetimeLocalValue(a.starts_at),
    ends_at: toDatetimeLocalValue(a.ends_at),
  }
}

export function announcementStatusLabel(
  a: Pick<Announcement, 'is_active' | 'starts_at' | 'ends_at'>,
  now: Date = new Date(),
): string {
  if (!a.is_active) return '已下线'
  if (a.starts_at && new Date(a.starts_at) > now) return '未开始'
  if (a.ends_at && new Date(a.ends_at) <= now) return '已过期'
  return '展示中'
}

// NOTE: 表单里的时间是本地时间，提交前转成 ISO（UTC）；留空提交 null（立即生效 / 永不过期）。其余字段原样透传，标题也不做 trim，与旧实现一致
export function toAnnouncementPayload(form: AnnouncementForm): AnnouncementPayload {
  return {
    ...form,
    starts_at: form.starts_at ? new Date(form.starts_at).toISOString() : null,
    ends_at: form.ends_at ? new Date(form.ends_at).toISOString() : null,
  }
}
