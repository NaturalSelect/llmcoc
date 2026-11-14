// NOTE: 线上模组（cnmods）接口类型，字段名与后端 handlers/online_module.go 一致。
// 来源站条目 CnmodsItem 保持来源站原样的 camelCase，本站自己的记录用 snake_case

export type OnlineStatus = 'pending' | 'downloading' | 'extracted' | 'failed'

// NOTE: 任务进度 WebSocket 的帧：{type, data}，只有服务端 → 客户端一个方向
export type OnlineTaskFrame =
  | { type: 'snapshot'; data: { modules: OnlineModule[] } }
  | { type: 'module'; data: OnlineModule }
  | { type: 'deleted'; data: { id: number } }

// 来源站条目；url 是蓝奏云分享页，opinion 是 HTML，只能当纯文本显示
export interface CnmodsItem {
  keyId: string
  title: string
  article: string
  moduleType: string
  moduleAge: string
  structure: string
  minAmount: number
  maxAmount: number
  minDuration: number
  maxDuration: number
  freeLevel: string
  releaseDate: string
  url: string
  opinion: string
  moduleVersion: string
  occurrencePlace: string
  original: string
}

export interface LocalRef {
  id: number
  status: OnlineStatus
  scenario_id: number | null
}

export type BrowseItem = CnmodsItem & { local: LocalRef | null }

// NOTE: 浏览接口不走统一分页信封：页码从 1 开始，总页数和总条数由来源站给出
export interface BrowsePage {
  items: BrowseItem[]
  page: number
  total_pages: number
  total_elements: number
}

export interface OnlineModuleMeta {
  module_age?: string
  structure?: string
  min_amount?: number
  max_amount?: number
  min_duration?: number
  max_duration?: number
  version?: string
  place?: string
  opinion?: string
}

export interface OnlineModule {
  id: number
  source: string
  source_key: string
  title: string
  author: string
  share_url: string
  meta: OnlineModuleMeta
  status: OnlineStatus
  // NOTE: error 非空即「最近一次后台任务失败」，与 status 无关：上传失败会恢复原状态，所以 extracted 也可能带 error
  error: string
  // stage：'' | 'resolve' | 'download' | 'extract' | 'ocr'；stage_note 是阶段备注（如正在 OCR 的文件名）
  stage: string
  stage_note: string
  // status 为 downloading 时才有值
  task_started_at: string | null
  archive_name: string
  archive_size: number
  archive_sha256: string
  license_override_by: number | null
  license_override_at: string | null
  scenario_id: number | null
  created_at: string
  updated_at: string
}

export interface OnlineModuleDocument {
  id: number
  online_module_id: number
  path: string
  kind: string
  runes: number
  candidate: boolean
  // 正文来自 OCR（PDF 本地抽取为空或乱码时的兜底识别）
  ocr: boolean
  // 正文疑似乱码且没能修复，不可编译
  garbled: boolean
  license_hits: string[]
  // 详情接口里只含前 300 字，全文要单独请求
  preview?: string
}

export interface OnlineModuleDocumentFull extends OnlineModuleDocument {
  text: string
}

export interface OnlineModuleDetail {
  module: OnlineModule
  documents: OnlineModuleDocument[]
}
