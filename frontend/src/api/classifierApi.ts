// NOTE: 分类器记录的类型见 classifierTypes.ts。管理员专用，非管理员调用会得到 403，调用方（分类器面板）只在管理员界面里挂载
import { normalizeClassifierLogs } from '@/game/classifier'
import type { ClassifierLog } from './classifierTypes'
import { api } from './client'

// NOTE: 后端默认 200、最大 500；面板只拉最近一屏，更早的记录用不上，实时事件会补上之后的
export const CLASSIFIER_HISTORY_LIMIT = 200

export const classifierApi = {
  // NOTE: Go 的 nil 切片会序列化成 null；每条记录都经过规整，不合法的丢弃，返回按 id 升序的列表（接口本身就是升序）
  list: async (sessionId: number, limit = CLASSIFIER_HISTORY_LIMIT): Promise<ClassifierLog[]> => {
    const res = await api<{ logs?: unknown } | null>('GET', `/api/admin/sessions/${sessionId}/classifier-logs?limit=${limit}`)
    return normalizeClassifierLogs(res?.logs)
  },
}
