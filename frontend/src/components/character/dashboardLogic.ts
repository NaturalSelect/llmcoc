import type { Announcement, RecentSession } from '@/api/characterTypes'

export const RECENT_SESSION_LIMIT = 6
export const ANNOUNCEMENT_COLLAPSED = 3

export type StampTone = 'seal' | 'ledger' | 'moss' | 'faint'

export function sessionStatusLabel(status: string | undefined): string {
  return status === 'lobby' ? '大厅' : status === 'playing' ? '游戏中' : '已结束'
}

export function sessionStatusTone(status: string | undefined): StampTone {
  return status === 'lobby' ? 'ledger' : status === 'playing' ? 'moss' : 'faint'
}

export function playersLabel(s: RecentSession): string {
  return `${s.players?.length ?? 0}/${s.max_players} 位调查员`
}

export function visibleAnnouncements(list: Announcement[], expanded: boolean): Announcement[] {
  return expanded ? list : list.slice(0, ANNOUNCEMENT_COLLAPSED)
}

export function announcementToggleLabel(expanded: boolean, total: number): string {
  return expanded ? '收起' : `展开全部（${total}）`
}

// NOTE: 只有 important / warning 有强调色，其余等级（含未知值）一律当普通公告
export function announcementAccent(level: string | undefined): string {
  if (level === 'important') return 'border-l-ledger'
  if (level === 'warning') return 'border-l-seal'
  return 'border-l-paper-edge'
}
