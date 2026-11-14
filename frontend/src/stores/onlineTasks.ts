import { defineStore } from 'pinia'
import { markRaw, ref } from 'vue'
import type { CompileLogResponse, OnlineModule } from '@/api/adminTypesOnline'
import { api } from '@/api/client'
import { OnlineTaskSocket } from '@/api/onlineTaskSocket'
import { useUiStore } from './ui'

export interface ModuleChange {
  module: OnlineModule
  settled: boolean
}

// NOTE: 与服务端 onlineLogMaxLines 一致：每个模组只保留最近 1000 行
const LOG_CAP = 1000

interface LogBuf {
  lines: Map<number, string>
  // NOTE: 目前见过的最大绝对行号，-1 表示还没有任何行
  max: number
}

function isCompile(m: Pick<OnlineModule, 'status' | 'stage'> | undefined): boolean {
  return m?.status === 'downloading' && m.stage === 'compile'
}

// NOTE: 线上模组后台任务（下载、上传提取、OCR、编译）的实时状态。数据只来自 WebSocket：快照整体替换，module 帧按 id 覆盖，deleted 帧移除。
// 连接按引用计数开关：任何一个线上模组页面挂载时打开，最后一个卸载时关闭，并清空状态，
// 这样下次打开的第一帧快照只是基线，不会把页面关闭期间完成的任务当成刚完成的再提示一遍。
// 编译日志也放在这里（而不是弹窗里），所以关掉弹窗再打开、或者换个页面都不会丢；刷新页面后靠 compile-log 接口补回。
export const useOnlineTasksStore = defineStore('onlineTasks', () => {
  const ui = useUiStore()
  const modules = ref<Record<number, OnlineModule>>({})
  const connected = ref(false)
  // NOTE: 已收到过快照：此前列表为空只代表还没加载，不代表没有任务
  const loaded = ref(false)

  const listeners = new Set<(change: ModuleChange) => void>()
  // NOTE: 已经提示过结束的状态（id → 结束那一帧的 updated_at）。upsertModule 在 202 比结束帧晚到时要补提示，靠它避免和帧触发的提示重复
  const announced = new Map<number, string>()
  let refs = 0
  // NOTE: socket 带着 WebSocket 和定时器，不能进响应式状态，所以只放在闭包里并 markRaw
  let socket: OnlineTaskSocket | null = null

  // NOTE: 日志不进响应式状态：每个模组最多 1000 行，逐行代理没有必要；写入时 logRev +1，compileLines 读它来订阅变化。
  // 按绝对行号存成 Map，实时 log 帧和 compile-log 响应可以任意顺序、重复合并
  const logs = new Map<number, LogBuf>()
  const logRev = ref(0)
  // NOTE: 每次快照或关闭连接 +1，之前发出的 compile-log 响应迟到时据此丢弃，避免把服务重启前的旧行写回来
  let logEpoch = 0
  // NOTE: 正在拉取日志的模组 id（计数）。快照到达时这些也要重拉，否则弹窗刚打开、响应被上面的 epoch 丢掉就永远没有日志
  const loadingLogs = new Map<number, number>()

  function isSettled(prev: OnlineModule | undefined, next: OnlineModule): boolean {
    return prev?.status === 'downloading' && next.status !== 'downloading'
  }

  // NOTE: 编译结束的提示和提取不同：编译成功不能说「已提取正文」。提示放在 store 里而不是弹窗里，
  // 这样每次结束只弹一次，且不管哪个页面（任务进度页、弹窗关着）都能看到
  function announce(m: OnlineModule, prev: OnlineModule | undefined): void {
    if (m.error !== '') ui.toast(m.title + '：' + m.error, 'error')
    else if (prev?.stage === 'compile') ui.toast('编译完成，已入库：' + m.title)
    else ui.toast('已提取正文：' + m.title)
  }

  function emit(module: OnlineModule, settled: boolean, prev: OnlineModule | undefined): void {
    if (settled) {
      announced.set(module.id, module.updated_at)
      announce(module, prev)
    }
    for (const fn of [...listeners]) fn({ module, settled })
  }

  function putLog(id: number, index: number, line: string): void {
    let buf = logs.get(id)
    if (!buf) {
      buf = { lines: new Map(), max: -1 }
      logs.set(id, buf)
    }
    // 已经滑出窗口的旧行不要
    if (index <= buf.max - LOG_CAP) return
    buf.lines.set(index, line)
    if (index > buf.max) buf.max = index
    if (buf.lines.size > LOG_CAP) {
      const floor = buf.max - LOG_CAP
      for (const k of buf.lines.keys()) if (k <= floor) buf.lines.delete(k)
    }
  }

  function applyLog(id: number, index: number, line: string): void {
    putLog(id, index, line)
    logRev.value++
  }

  // NOTE: 返回最新的连续一段：从最大行号往下数，遇到缺口就停。缺口以下的行即使存在也不显示，
  // 否则 GET 还没回来时先到的实时帧会和旧行拼成跳行的日志
  function compileLines(id: number): string[] {
    void logRev.value
    const buf = logs.get(id)
    if (!buf || buf.max < 0) return []
    const out: string[] = []
    for (let i = buf.max; ; i--) {
      const line = buf.lines.get(i)
      if (line === undefined) break
      out.push(line)
    }
    return out.reverse()
  }

  // NOTE: 拉取失败不抛给调用方（日志加载失败不应该让弹窗坏掉），只返回是否成功合并
  async function loadCompileLog(id: number): Promise<boolean> {
    const epoch = logEpoch
    loadingLogs.set(id, (loadingLogs.get(id) ?? 0) + 1)
    try {
      const resp = await api<CompileLogResponse>('GET', `/api/admin/online-modules/${id}/compile-log`)
      if (epoch !== logEpoch) return false
      if (!resp || !Number.isInteger(resp.base) || resp.base < 0 || !Array.isArray(resp.lines)) return false
      // 服务端本来就只留 1000 行；这里再截一次，防止异常响应把 Map 撑大
      const from = Math.max(0, resp.lines.length - LOG_CAP)
      for (let i = from; i < resp.lines.length; i++) {
        const line = resp.lines[i]
        if (typeof line === 'string') putLog(id, resp.base + i, line)
      }
      logRev.value++
      return true
    } catch {
      return false
    } finally {
      const left = (loadingLogs.get(id) ?? 1) - 1
      if (left > 0) loadingLogs.set(id, left)
      else loadingLogs.delete(id)
    }
  }

  function applySnapshot(list: OnlineModule[]): void {
    const prev = modules.value
    const first = !loaded.value
    const next: Record<number, OnlineModule> = {}
    for (const m of list) next[m.id] = m
    modules.value = next
    loaded.value = true

    // NOTE: 快照也是每次重连的第一帧：服务端可能重启过（日志只在内存里，行号会从 0 重来），旧行不能留。
    // 先记下哪些模组需要日志（已有日志、正在编译、拉取在途），清空后重新拉
    const reload = new Set<number>(logs.keys())
    for (const m of list) if (isCompile(m)) reload.add(m.id)
    for (const id of loadingLogs.keys()) reload.add(id)
    logs.clear()
    logEpoch++
    logRev.value++

    // NOTE: 首帧快照只建立基线，不提示；重连后的快照可以补发断线期间完成的任务
    for (const m of list) emit(m, !first && isSettled(prev[m.id], m), prev[m.id])
    for (const id of reload) void loadCompileLog(id)
  }

  function applyModule(m: OnlineModule): void {
    const prev = modules.value[m.id]
    modules.value[m.id] = m
    emit(m, isSettled(prev, m), prev)
  }

  function applyDeleted(id: number): void {
    delete modules.value[id]
    announced.delete(id)
    if (logs.delete(id)) logRev.value++
  }

  // NOTE: 把 HTTP 响应里的模组（如编译请求的 202）写进 store，让任务页立刻看到。和 WebSocket 帧不同，响应可能比已经到达的帧旧：
  // store 里已有更新的 updated_at 时丢弃，避免把已结束的任务退回「进行中」；时间相同时只有已结束的状态才算更新
  // （与 upload() 对 202 竞态的判断一致）。返回是否采用
  function upsertModule(m: OnlineModule): boolean {
    const prev = modules.value[m.id]
    if (prev) {
      const pt = Date.parse(prev.updated_at)
      const nt = Date.parse(m.updated_at)
      if (pt > nt || (pt === nt && prev.status !== 'downloading')) {
        // NOTE: 任务在 202 到达之前就结束了：如果 store 没见过 downloading 帧，isSettled 不会触发，结果提示只能在这里补。
        // 202 本身就是「这次任务确实跑过」的证据，所以可以当作 downloading → 已结束；已经提示过的不再重复
        if (prev.status !== 'downloading' && m.status === 'downloading' && announced.get(prev.id) !== prev.updated_at) {
          announced.set(prev.id, prev.updated_at)
          announce(prev, m)
        }
        return false
      }
    }
    applyModule(m)
    return true
  }

  function start(): void {
    socket = markRaw(
      new OnlineTaskSocket({
        onSnapshot: applySnapshot,
        onModule: applyModule,
        onDeleted: applyDeleted,
        onLog: applyLog,
        onStatus: (v) => {
          connected.value = v
        },
      }),
    )
    socket.open()
  }

  function stop(): void {
    socket?.close()
    socket = null
    modules.value = {}
    connected.value = false
    loaded.value = false
    logs.clear()
    loadingLogs.clear()
    announced.clear()
    logEpoch++
    logRev.value++
  }

  function acquire(): void {
    refs++
    if (refs === 1) start()
  }

  function release(): void {
    if (refs === 0) return
    refs--
    if (refs === 0) stop()
  }

  // NOTE: 订阅每一次模块变化（含快照里的每个模块）；settled 表示上一次已知状态是 downloading、这一次不是。返回取消订阅函数，组件卸载时必须调用
  function onChange(fn: (change: ModuleChange) => void): () => void {
    listeners.add(fn)
    return () => {
      listeners.delete(fn)
    }
  }

  return { modules, connected, loaded, acquire, release, onChange, compileLines, loadCompileLog, upsertModule }
})
