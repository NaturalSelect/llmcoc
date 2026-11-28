// NOTE: System One 分类器记录的结构，按后端契约整理：管理员接口 GET /api/admin/sessions/:id/classifier-logs 返回 { logs }，
// 房间 WebSocket 的临时事件（seq=0）classifier_log 的 data 为 { log }（只有管理员连接会收到，不补发）。两条路径是同一种记录，按 id 去重合并

// NOTE: input_guard 是玩家提交行动前的越权/索要谜底/提示词注入分类；nsfw_route 是 KP 周期开始前的 NSFW 路由分类
export type ClassifierKind = 'input_guard' | 'nsfw_route'

export interface ClassifierLog {
  // NOTE: 房间内全局唯一的自增 id，用来去重和排序
  id: number
  kind: ClassifierKind
  // NOTE: RFC 3339
  created_at: string
  // NOTE: input_guard 是提交者用户 id；nsfw_route 恒为 0
  user_id: number
  // NOTE: input_guard 是提交者扮演的调查员名；nsfw_route 是本周期提交行动的调查员名（多个用「、」连接），可为空串
  subject: string
  // NOTE: nsfw_route 的周期编号；input_guard 恒为 0
  floor: number
  // NOTE: input_guard 只会是 blocked（已拒收）/ passed（放行，含分类器出错时的放行）；
  // nsfw_route 只会是 nsfw（本周期用 director_nsfw）/ clean（用 director）/ failed（调用失败，沿用了上个周期的角色）。
  // 不收窄成联合类型：后端以后加新取值时，前端按原文显示而不是丢记录
  decision: string
  // NOTE: error 表示调用失败、超时或响应异常，此时 choice 为空串、probabilities 为空对象、score 为 0
  status: 'ok' | 'error'
  // NOTE: status=error 时的人读原因，ok 时为空串
  error: string
  // NOTE: 概率最高的选项名
  choice: string
  // NOTE: 判定用的分数。input_guard 是 authority_claim + spoiler_request + prompt_injection 三项之和，
  // nsfw_route 是 sexual_explicit + sexual_suggestive 两项之和
  score: number
  // NOTE: 判定当时生效的阈值，score >= threshold 为命中
  threshold: number
  // NOTE: 每个选项的概率，键是选项名
  probabilities: Record<string, number>
  // NOTE: 当时发给分类器的 state，值可能是多行文本；内容来自玩家和 KP，是不可信文本，只能做文本插值
  request: Record<string, string>
  // NOTE: nsfw_route 是本周期实际执行的 Director 角色（director / director_nsfw）；input_guard 为空串
  role: string
  elapsed_ms: number
  request_id: string
}
