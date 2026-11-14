import { defineStore } from 'pinia'
import { markRaw, ref } from 'vue'
import type { OnlineModule } from '@/api/adminTypesOnline'
import { OnlineTaskSocket } from '@/api/onlineTaskSocket'
import { useUiStore } from './ui'

export interface ModuleChange {
  module: OnlineModule
  settled: boolean
}

// NOTE: 线上模组后台任务（下载、上传提取、OCR）的实时状态。数据只来自 WebSocket：快照整体替换，module 帧按 id 覆盖，deleted 帧移除。
// 连接按引用计数开关：任何一个线上模组页面挂载时打开，最后一个卸载时关闭，并清空状态，
// 这样下次打开的第一帧快照只是基线，不会把页面关闭期间完成的任务当成刚完成的再提示一遍。
export const useOnlineTasksStore = defineStore('onlineTasks', () => {
  const ui = useUiStore()
  const modules = ref<Record<number, OnlineModule>>({})
  const connected = ref(false)
  // NOTE: 已收到过快照：此前列表为空只代表还没加载，不代表没有任务
  const loaded = ref(false)

  const listeners = new Set<(change: ModuleChange) => void>()
  let refs = 0
  // NOTE: socket 带着 WebSocket 和定时器，不能进响应式状态，所以只放在闭包里并 markRaw
  let socket: OnlineTaskSocket | null = null

  function isSettled(prev: OnlineModule | undefined, next: OnlineModule): boolean {
    return prev?.status === 'downloading' && next.status !== 'downloading'
  }

  function announce(m: OnlineModule): void {
    if (m.error === '') ui.toast('已提取正文：' + m.title)
    else ui.toast(m.title + '：' + m.error, 'error')
  }

  function emit(module: OnlineModule, settled: boolean): void {
    if (settled) announce(module)
    for (const fn of [...listeners]) fn({ module, settled })
  }

  function applySnapshot(list: OnlineModule[]): void {
    const prev = modules.value
    const first = !loaded.value
    const next: Record<number, OnlineModule> = {}
    for (const m of list) next[m.id] = m
    modules.value = next
    loaded.value = true
    // NOTE: 首帧快照只建立基线，不提示；重连后的快照可以补发断线期间完成的任务
    for (const m of list) emit(m, !first && isSettled(prev[m.id], m))
  }

  function applyModule(m: OnlineModule): void {
    const prev = modules.value[m.id]
    modules.value[m.id] = m
    emit(m, isSettled(prev, m))
  }

  function applyDeleted(id: number): void {
    delete modules.value[id]
  }

  function start(): void {
    socket = markRaw(
      new OnlineTaskSocket({
        onSnapshot: applySnapshot,
        onModule: applyModule,
        onDeleted: applyDeleted,
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

  return { modules, connected, loaded, acquire, release, onChange }
})
