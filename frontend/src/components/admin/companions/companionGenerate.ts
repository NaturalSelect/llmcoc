import type { CompanionGenBatchDone, CompanionGenDone, CompanionGenError, CompanionGenStart } from '@/api/companionTypes'

// NOTE: AI 卡池批量生成 WebSocket 的纯逻辑：事件 → 日志行、流结束后的汇总提示。不碰 DOM、不发请求，方便单测

// 与后端 maxCompanionGenCount 保持一致
export const MAX_COMPANION_GEN_COUNT = 10

export interface CompanionGenOutcome {
  logs: string[]
  toast: { message: string; type: 'success' | 'error' }
  reload: boolean
}

function parse<T>(data: string): T | null {
  try {
    return JSON.parse(data) as T
  } catch {
    return null
  }
}

export function createCompanionGenHandler(log: (line: string) => void) {
  let batchDone: CompanionGenBatchDone | null = null

  function onEvent(event: string, data: string): void {
    switch (event) {
      case 'card_start': {
        const info = parse<CompanionGenStart>(data)
        if (info) log(`第 ${info.index} 张开始生成：${info.name}（${info.gender}，${info.age} 岁，${info.occupation}）`)
        break
      }
      case 'card_done': {
        const info = parse<CompanionGenDone>(data)
        if (info) log(`第 ${info.index} 张完成：${info.name}（${info.occupation}），已放入卡池`)
        break
      }
      case 'card_error': {
        const info = parse<CompanionGenError>(data)
        if (info) log(`第 ${info.index} 张失败：${info.name ? info.name + '，' : ''}${info.message || '生成失败'}`)
        break
      }
      case 'batch_done':
        batchDone = parse<CompanionGenBatchDone>(data)
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
