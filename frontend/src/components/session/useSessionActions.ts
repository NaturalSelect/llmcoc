import { ref } from 'vue'
import { api } from '@/api/client'
import type { LobbySession, MessageResponse } from '@/api/sessionTypes'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'
import { endSessionConfirmMessage, errorMessage, reviveConfirmMessage } from './sessionLogic'

const DEFAULT_END_COST = 200

type SessionRef = Pick<LobbySession, 'id'>

// NOTE: 房间的退出/开始/结束/复活/收藏动作，大厅和游戏页顶栏共用同一份确认文案、提示文案和调用顺序。
// 列表刷新由调用方通过回调传入；回调放在 try 里执行，与旧前端一致（刷新失败也弹错误提示）。
// startSession 只负责 /start：房主开场需要再触发一次 kp-turn，那是游戏页 roomController.openScene 的职责。
// endSession 读取 auth.shopCosts.end_session_cost；费率还没加载时先拉一次（失败会回落为空对象），缺省按 200 计。
export function useSessionActions() {
  const auth = useAuthStore()
  const ui = useUiStore()
  const leaving = ref(false)
  const ending = ref(false)
  const reviving = ref(false)

  async function leaveSession(session: SessionRef | null | undefined, afterLeave?: () => Promise<void>): Promise<boolean> {
    if (!session?.id) return false
    if (!(await ui.confirm('确认退出该房间？', { danger: true, confirmText: '退出' }))) return false
    leaving.value = true
    try {
      const r = await api<MessageResponse>('POST', `/api/sessions/${session.id}/leave`)
      ui.toast(r?.message || '已退出房间')
      await afterLeave?.()
      return true
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
      return false
    } finally {
      leaving.value = false
    }
  }

  async function startSession(session: SessionRef): Promise<boolean> {
    try {
      await api('POST', `/api/sessions/${session.id}/start`)
      ui.toast('游戏已开始！')
      return true
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
      return false
    }
  }

  async function endSession(session: Pick<LobbySession, 'id' | 'players'>): Promise<boolean> {
    const playerCount = session.players?.length || 0
    if (auth.shopCosts.end_session_cost === undefined) await auth.loadShopCosts()
    const cost = auth.shopCosts.end_session_cost ?? DEFAULT_END_COST
    if (!(await ui.confirm(endSessionConfirmMessage(playerCount, cost), { confirmText: '结束游戏' }))) return false
    ending.value = true
    try {
      const r = await api<MessageResponse>('POST', `/api/sessions/${session.id}/end`)
      ui.toast(r?.message || '游戏已结束，角色成长已结算。')
      return true
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
      return false
    } finally {
      ending.value = false
    }
  }

  async function reviveSession(session: (SessionRef & { name?: string }) | null | undefined, afterRevive?: () => Promise<void>): Promise<boolean> {
    if (!session?.id) return false
    if (!(await ui.confirm(reviveConfirmMessage(session), { confirmText: '复活' }))) return false
    reviving.value = true
    try {
      const r = await api<MessageResponse>('POST', `/api/sessions/${session.id}/revive`)
      await afterRevive?.()
      ui.toast(r?.message || '房间已复活')
      return true
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
      return false
    } finally {
      reviving.value = false
    }
  }

  async function addFavorite(id: number, afterAdd?: () => Promise<void>): Promise<boolean> {
    try {
      await api('POST', `/api/sessions/${id}/favorite`)
      ui.toast('已收藏')
      await afterAdd?.()
      return true
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
      return false
    }
  }

  async function removeFavorite(id: number, afterRemove?: () => Promise<void>): Promise<boolean> {
    try {
      await api('DELETE', `/api/sessions/${id}/favorite`)
      ui.toast('已取消收藏')
      await afterRemove?.()
      return true
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
      return false
    }
  }

  return { leaving, ending, reviving, leaveSession, startSession, endSession, reviveSession, addFavorite, removeFavorite }
}
