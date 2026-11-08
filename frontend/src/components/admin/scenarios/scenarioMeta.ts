import { difficultyLabel } from '@/utils/format'
import type { AdminScenario } from '@/api/adminTypesContent'

export function scenarioMeta(s: AdminScenario): string {
  return (
    difficultyLabel(s.difficulty) +
    ' · ' +
    (s.min_players || 1) +
    '-' +
    (s.max_players || 4) +
    '人' +
    (s.author ? ' · ' + s.author : '')
  )
}
