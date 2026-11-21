import type {
  CompanionGenBatchDone,
  CompanionGenDone,
  CompanionGenError,
  CompanionGenSnapshot,
  CompanionGenStart,
} from '@/api/companionTypes'

// NOTE: AI 卡池批量生成 WebSocket 的纯逻辑：事件 → 日志行、流结束后的汇总提示。不碰 DOM、不发请求，方便单测

// 与后端 maxCompanionGenCount 保持一致
export const MAX_COMPANION_GEN_COUNT = 10

export interface CompanionGenOutcome {
  logs: string[]
  toast: { message: string; type: 'success' | 'error' }
  reload: boolean
}

// at 是事件发生时的毫秒时间戳，只有回放 snapshot 里的历史事件时才有
export type CompanionGenLog = (line: string, at?: number) => void

export interface CompanionGenHooks {
  // snapshot 到达时先于回放调用：页面据此清掉旧日志、同步「任务是否还在跑」
  onSnapshot?: (snapshot: CompanionGenSnapshot) => void
}

function parse<T>(data: string): T | null {
  try {
    return JSON.parse(data) as T
  } catch {
    return null
  }
}

export function createCompanionGenHandler(log: CompanionGenLog, hooks: CompanionGenHooks = {}) {
  let batchDone: CompanionGenBatchDone | null = null
  let snapshot: CompanionGenSnapshot | null = null

  function apply(event: string, payload: unknown, at?: number): void {
    switch (event) {
      case 'card_start': {
        const info = payload as CompanionGenStart | null
        if (info) log(`第 ${info.index} 张开始生成：${info.name}（${info.gender}，${info.age} 岁，${info.occupation}）`, at)
        break
      }
      case 'card_done': {
        const info = payload as CompanionGenDone | null
        if (info) log(`第 ${info.index} 张完成：${info.name}（${info.occupation}），已放入卡池`, at)
        break
      }
      case 'card_error': {
        const info = payload as CompanionGenError | null
        if (info) log(`第 ${info.index} 张失败：${info.name ? info.name + '，' : ''}${info.message || '生成失败'}`, at)
        break
      }
      case 'batch_done':
        batchDone = payload as CompanionGenBatchDone | null
        break
      case 'snapshot': {
        const info = payload as CompanionGenSnapshot | null
        if (!info || !Array.isArray(info.events)) break
        snapshot = info
        hooks.onSnapshot?.(info)
        for (const e of info.events) {
          if (e.type !== 'snapshot') apply(e.type, e.data, e.at)
        }
        break
      }
    }
  }

  function onEvent(event: string, data: string): void {
    apply(event, parse<unknown>(data))
  }

  return {
    onEvent,
    get batchDone() {
      return batchDone
    },
    // 只有 attach 连接会收到；null 表示这条连接是自己启动的任务
    get snapshot() {
      return snapshot
    },
  }
}

// NOTE: 没收到 batch_done 说明连接中途断开：后台仍在生成，只提示稍后刷新卡池
export function summarizeCompanionGen(batchDone: CompanionGenBatchDone | null): CompanionGenOutcome {
  if (!batchDone) {
    return {
      logs: ['连接中断，生成仍在后台继续，稍后请刷新卡池'],
      toast: { message: '连接中断，生成仍在后台继续', type: 'error' },
      reload: false,
    }
  }
  const text = `成功 ${batchDone.succeeded} 张，失败 ${batchDone.failed} 张`
  return {
    logs: [`批量生成完成：${text}`],
    toast: { message: `批量生成完成：${text}`, type: batchDone.failed > 0 ? 'error' : 'success' },
    reload: batchDone.succeeded > 0,
  }
}
