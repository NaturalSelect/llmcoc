// NOTE: 把分类器记录摆进消息流的纯逻辑（只给管理员用）：输入分类挂在对应玩家消息下面，NSFW 路由挂在该周期最后一条玩家消息下面，
// 还没成为任何玩家消息的输入分类（floor 为 0：被拦截、被取代、还在等其他玩家）按时间放在紧挨着的前一条消息之后。
// 一次遍历消息、一次遍历记录，游戏页每次消息或记录变化只算一遍，MessageItem 不用自己扫整个列表
import type { ClassifierLog } from '@/api/classifierTypes'
import { msgKind } from './messages'
import type { RoomMessage } from './roomState'

export interface ClassifierPlacement {
  // NOTE: 键是玩家消息 id；值里输入分类在前、路由在后，各自按 id 升序
  byMessage: Map<number, ClassifierLog[]>
  // NOTE: 孤儿记录：没有前一条消息的放最前面
  leading: ClassifierLog[]
  // NOTE: 孤儿记录：键是它前面那条消息的 id
  after: Map<number, ClassifierLog[]>
}

export function emptyPlacement(): ClassifierPlacement {
  return { byMessage: new Map(), leading: [], after: new Map() }
}

function append<K>(map: Map<K, ClassifierLog[]>, key: K, ...logs: ClassifierLog[]): void {
  const list = map.get(key)
  if (list) list.push(...logs)
  else map.set(key, [...logs])
}

function timeOf(v: unknown): number {
  return typeof v === 'string' ? Date.parse(v) : Number.NaN
}

// NOTE: 取到当前位置为止见过的最晚时间，缺时间或乱序的消息沿用前面的值，保证数组单调不降、能二分；开头还没见到有效时间的是 -Infinity
function runningTimes(messages: readonly RoomMessage[]): number[] {
  const out: number[] = []
  let run = Number.NEGATIVE_INFINITY
  for (const m of messages) {
    const t = timeOf(m.created_at)
    if (!Number.isNaN(t) && t > run) run = t
    out.push(run)
  }
  return out
}

// NOTE: 最后一条不晚于 t 的消息的下标，没有返回 -1；记录自己的时间不可解析时放到末尾（最新的一条之后）
function anchorIndex(times: readonly number[], t: number): number {
  if (Number.isNaN(t)) return times.length - 1
  let lo = 0
  let hi = times.length
  while (lo < hi) {
    const mid = (lo + hi) >>> 1
    if (times[mid] <= t) lo = mid + 1
    else hi = mid
  }
  return lo - 1
}

// NOTE: messages 是房间里完整的消息列表（按 id 升序）；孤儿记录的锚点必定在其中，调用方渲染时不能过滤掉它（小说模式下整体不渲染孤儿行）。
// 输入分类按「周期 + 用户」匹配玩家消息，同一玩家同一周期有多条玩家消息时挂在最后一条；
// floor 不是正数的路由记录没有对应的周期，不展示
export function placeClassifierLogs(messages: readonly RoomMessage[], logs: readonly ClassifierLog[]): ClassifierPlacement {
  const out = emptyPlacement()
  if (logs.length === 0) return out

  const guards = new Map<string, ClassifierLog[]>()
  const routes = new Map<number, ClassifierLog[]>()
  const orphans: ClassifierLog[] = []
  for (const log of logs) {
    if (log.kind === 'input_guard') {
      if (log.floor > 0) append(guards, `${log.floor}:${log.user_id}`, log)
      else if (log.floor === 0) orphans.push(log)
    } else if (log.kind === 'nsfw_route' && log.floor > 0) {
      append(routes, log.floor, log)
    }
  }

  if (guards.size > 0 || routes.size > 0) {
    const lastByGuard = new Map<string, number>()
    const lastByFloor = new Map<number, number>()
    for (const m of messages) {
      if (msgKind(m) !== 'player') continue
      const floor = Number(m.floor)
      if (!(floor > 0)) continue
      lastByFloor.set(floor, m.id)
      lastByGuard.set(`${floor}:${Number(m.user_id)}`, m.id)
    }
    for (const [key, list] of guards) {
      const id = lastByGuard.get(key)
      if (id !== undefined) append(out.byMessage, id, ...list)
    }
    for (const [floor, list] of routes) {
      const id = lastByFloor.get(floor)
      if (id !== undefined) append(out.byMessage, id, ...list)
    }
  }

  if (orphans.length > 0) {
    const times = runningTimes(messages)
    for (const log of orphans) {
      const idx = anchorIndex(times, timeOf(log.created_at))
      if (idx < 0) out.leading.push(log)
      else append(out.after, messages[idx].id, log)
    }
  }
  return out
}
