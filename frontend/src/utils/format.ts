export function fmtTime(iso?: string | null): string {
  if (!iso) return ''
  return new Date(iso).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

export function fmtDate(iso?: string | null): string {
  if (!iso) return ''
  return new Date(iso).toLocaleDateString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function fmtDuration(ms?: number | null): string {
  if (!ms) return ''
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

export function fmtStepAvg(ms?: number | null, steps?: number | null): string {
  if (!ms || !steps) return ''
  return fmtDuration(Math.round(ms / steps))
}

const DIFFICULTY: Record<string, string> = { easy: '简单', normal: '普通', hard: '困难' }

export function difficultyLabel(v?: string | null): string {
  const key = (v || 'normal').toLowerCase()
  return DIFFICULTY[key] || (v as string)
}
