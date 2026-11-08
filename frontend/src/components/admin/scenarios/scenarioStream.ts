import type {
  BatchDone,
  BatchProgressInfo,
  GenDoneInfo,
  GenErrorInfo,
  GenProgressInfo,
} from '@/api/adminTypesContent'

// NOTE: 模组生成与故事编译两条 SSE 流的纯逻辑：事件 → 日志行、流结束后的汇总提示。
// 文案与旧 admin.js 逐字一致；这里不碰 DOM、不发请求，方便单测

export interface StreamOutcome {
  logs: string[]
  toast: { message: string; type: 'success' | 'error' }
  reload: boolean
}

export function formatLogLine(line: string, now: Date = new Date()): string {
  const time = now.toLocaleTimeString('zh-CN', { hour12: false })
  return '[' + time + '] ' + line
}

// NOTE: exchange 事件展示 LLM 交互摘要，其余事件优先显示 detail。
// info.stage 缺失时会拼出 "undefined "，这是旧实现的行为，保持不变
export function progressLine(info: unknown): string {
  if (!info || typeof info !== 'object') return String(info || '')
  const p = info as GenProgressInfo
  if (p.stage === 'exchange') {
    return '· LLM 交互 [' + (p.status || '') + '] ' + (p.detail || '')
  }
  return p.detail || p.stage + ' ' + (p.status || '')
}

export function progressDataLine(data: string): string {
  try {
    return progressLine(JSON.parse(data))
  } catch {
    return data
  }
}

export function createGenerationHandler(log: (line: string) => void) {
  let batchDone: BatchDone | null = null

  function onEvent(event: string, data: string): void {
    switch (event) {
      case 'progress':
        log(progressDataLine(data))
        break
      case 'batch_progress':
        try {
          const info = JSON.parse(data) as BatchProgressInfo
          log('📦 开始生成第 ' + info.current + '/' + info.total + ' 个模组…')
        } catch {
          // NOTE: 畸形的 batch_progress 直接忽略，不影响后续事件
        }
        break
      case 'done':
        try {
          const info = JSON.parse(data) as GenDoneInfo
          log('✅ 第 ' + (info.index || 1) + ' 个生成完成：《' + (info.name || '未命名') + '》，已入库')
        } catch {
          // NOTE: 畸形的 done 直接忽略，最终结果以 batch_done 为准
        }
        break
      case 'error':
        try {
          const info = JSON.parse(data) as GenErrorInfo
          log('❌ 第 ' + (info.index || 1) + ' 个生成失败：' + (info.message || '生成失败'))
        } catch {
          log('❌ ' + (data || '生成失败'))
        }
        break
      case 'batch_done':
        try {
          batchDone = JSON.parse(data) as BatchDone
        } catch {
          batchDone = null
        }
        break
    }
  }

  return {
    onEvent,
    get batchDone() {
      return batchDone
    },
  }
}

// NOTE: 流结束后的收尾。没收到 batch_done 说明连接中途断开：后台仍在生成，只提示稍后刷新
export function summarizeBatchDone(batchDone: BatchDone | null): StreamOutcome {
  if (!batchDone) {
    return {
      logs: ['⚠️ 连接中断，生成仍在后台继续，稍后请刷新模组列表'],
      toast: { message: '连接中断，生成仍在后台继续', type: 'error' },
      reload: false,
    }
  }
  const reload = batchDone.succeeded > 0
  if (batchDone.total > 1) {
    const text = '成功 ' + batchDone.succeeded + ' 个，失败 ' + batchDone.failed + ' 个'
    return {
      logs: ['🏁 批量生成完成：' + text],
      toast:
        batchDone.failed > 0
          ? { message: '批量生成完成：' + text, type: 'error' }
          : { message: '批量生成完成：成功 ' + batchDone.succeeded + ' 个', type: 'success' },
      reload,
    }
  }
  if (batchDone.succeeded > 0) {
    return { logs: [], toast: { message: 'AI 模组生成并入库成功', type: 'success' }, reload }
  }
  return { logs: [], toast: { message: batchDone.results?.[0]?.error || '生成失败', type: 'error' }, reload }
}

export function createCompileHandler(log: (line: string) => void) {
  let done: GenDoneInfo | null = null
  let error: GenErrorInfo | null = null

  function onEvent(event: string, data: string): void {
    switch (event) {
      case 'progress':
        log(progressDataLine(data))
        break
      case 'done':
        try {
          done = JSON.parse(data) as GenDoneInfo
        } catch {
          done = null
        }
        break
      case 'error':
        try {
          error = JSON.parse(data) as GenErrorInfo
        } catch {
          error = { message: data || '编译失败' }
        }
        break
    }
  }

  return {
    onEvent,
    get done() {
      return done
    },
    get error() {
      return error
    },
  }
}

export function summarizeCompile(done: GenDoneInfo | null, error: GenErrorInfo | null): StreamOutcome {
  if (done) {
    return {
      logs: ['✅ 编译完成：《' + (done.name || '未命名') + '》，已入库'],
      toast: { message: '故事编译并入库成功', type: 'success' },
      reload: true,
    }
  }
  if (error) {
    return {
      logs: ['❌ ' + (error.message || '编译失败')],
      toast: { message: error.message || '编译失败', type: 'error' },
      reload: false,
    }
  }
  return {
    logs: ['⚠️ 连接中断，编译仍在后台继续，稍后请刷新模组列表'],
    toast: { message: '连接中断，编译仍在后台继续', type: 'error' },
    reload: false,
  }
}

export const MAX_STORY_BYTES = 512 * 1024

export function validateStoryFile(file: { name: string; size: number }): string | null {
  if (!/\.(md|markdown|txt)$/i.test(file.name)) return '仅支持 .md / .markdown / .txt 格式'
  if (file.size > MAX_STORY_BYTES) return '文件过大（最大 500KB）'
  return null
}
