// NOTE: 模组详情弹窗在跑团大厅和后台模组页共用，字段按后端 Scenario JSON 补全；content 内部结构较深，用宽松类型承接
export interface Scenario {
  id: number
  name: string
  difficulty?: string
  min_players?: number
  max_players?: number
  author?: string
  tags?: string
  content?: Record<string, any>
  [key: string]: unknown
}
