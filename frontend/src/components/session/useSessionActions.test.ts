// @vitest-environment jsdom
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'
import { useSessionActions } from './useSessionActions'

const apiMock = vi.hoisted(() => vi.fn())
vi.mock('@/api/client', async (orig) => ({ ...(await orig<typeof import('@/api/client')>()), api: apiMock }))

let ui: ReturnType<typeof useUiStore>
let auth: ReturnType<typeof useAuthStore>

const toasts = () => ui.toasts.map((t) => [t.message, t.type])
// 让下一次 confirm 立即以 ok 结算，并记下弹窗参数
function answerConfirm(ok: boolean) {
  const spy = vi.spyOn(ui, 'confirm').mockResolvedValue(ok as never)
  return spy
}

beforeEach(() => {
  setActivePinia(createPinia())
  apiMock.mockReset()
  ui = useUiStore()
  auth = useAuthStore()
  auth.shopCosts = { end_session_cost: 200 }
})

describe('leaveSession', () => {
  test('确认框用危险样式；取消时不发请求', async () => {
    const confirm = answerConfirm(false)
    const { leaveSession } = useSessionActions()
    expect(await leaveSession({ id: 4 })).toBe(false)
    expect(confirm).toHaveBeenCalledWith('确认退出该房间？', { danger: true, confirmText: '退出' })
    expect(apiMock).not.toHaveBeenCalled()
  })

  test('成功：提示服务端 message，之后执行刷新回调', async () => {
    answerConfirm(true)
    apiMock.mockResolvedValue({ message: '退出房间成功,房间无人已自动解散' })
    const after = vi.fn().mockResolvedValue(undefined)
    const a = useSessionActions()
    expect(await a.leaveSession({ id: 4 }, after)).toBe(true)
    expect(apiMock).toHaveBeenCalledWith('POST', '/api/sessions/4/leave')
    expect(toasts()).toEqual([['退出房间成功,房间无人已自动解散', 'success']])
    expect(after).toHaveBeenCalledOnce()
    expect(a.leaving.value).toBe(false)
  })

  test('服务端没返回 message 时用默认文案', async () => {
    answerConfirm(true)
    apiMock.mockResolvedValue({})
    await useSessionActions().leaveSession({ id: 4 })
    expect(toasts()).toEqual([['已退出房间', 'success']])
  })

  test('失败：弹错误提示并复位 leaving', async () => {
    answerConfirm(true)
    apiMock.mockRejectedValue(new ApiError('你不在此房间中', 400))
    const a = useSessionActions()
    expect(await a.leaveSession({ id: 4 })).toBe(false)
    expect(toasts()).toEqual([['你不在此房间中', 'error']])
    expect(a.leaving.value).toBe(false)
  })

  test('没有房间 id 时什么也不做', async () => {
    const confirm = answerConfirm(true)
    expect(await useSessionActions().leaveSession(null)).toBe(false)
    expect(confirm).not.toHaveBeenCalled()
  })
})

describe('startSession', () => {
  test('成功提示「游戏已开始！」', async () => {
    apiMock.mockResolvedValue({ message: '游戏已开始' })
    expect(await useSessionActions().startSession({ id: 9 })).toBe(true)
    expect(apiMock).toHaveBeenCalledWith('POST', '/api/sessions/9/start')
    expect(toasts()).toEqual([['游戏已开始！', 'success']])
  })

  test('失败返回 false 并提示错误', async () => {
    apiMock.mockRejectedValue(new ApiError('只有房主可以开始游戏', 403))
    expect(await useSessionActions().startSession({ id: 9 })).toBe(false)
    expect(toasts()).toEqual([['只有房主可以开始游戏', 'error']])
  })
})

describe('endSession', () => {
  test('确认文案按人数和费率计算，确认按钮不是危险样式', async () => {
    const confirm = answerConfirm(false)
    auth.shopCosts = { end_session_cost: 150 }
    await useSessionActions().endSession({ id: 2, players: [{ user_id: 1 }, { user_id: 2 }] })
    expect(confirm).toHaveBeenCalledWith('确认结束游戏？2名玩家每人将消耗150金币（共300金币），系统将进行成长评估。', { confirmText: '结束游戏' })
    expect(apiMock).not.toHaveBeenCalled()
  })

  test('费率未加载时先拉取；仍拿不到则按 200 计', async () => {
    auth.shopCosts = {}
    vi.spyOn(auth, 'loadShopCosts').mockResolvedValue(undefined)
    const confirm = answerConfirm(false)
    await useSessionActions().endSession({ id: 2, players: null })
    expect(auth.loadShopCosts).toHaveBeenCalledOnce()
    expect(confirm).toHaveBeenCalledWith('确认结束游戏？将消耗200金币，系统将进行成长评估。', { confirmText: '结束游戏' })
  })

  test('成功：提示服务端 message 并复位 ending', async () => {
    answerConfirm(true)
    apiMock.mockResolvedValue({ message: '游戏已结束' })
    const a = useSessionActions()
    expect(await a.endSession({ id: 2, players: [] })).toBe(true)
    expect(apiMock).toHaveBeenCalledWith('POST', '/api/sessions/2/end')
    expect(toasts()).toEqual([['游戏已结束', 'success']])
    expect(a.ending.value).toBe(false)
  })

  test('金币不足等错误：提示错误文案', async () => {
    answerConfirm(true)
    apiMock.mockRejectedValue(new ApiError('金币不足，结束游戏每人需要消耗200金币', 402))
    const a = useSessionActions()
    expect(await a.endSession({ id: 2, players: [] })).toBe(false)
    expect(toasts()).toEqual([['金币不足，结束游戏每人需要消耗200金币', 'error']])
    expect(a.ending.value).toBe(false)
  })
})

describe('reviveSession', () => {
  test('确认文案带房间名；成功后先刷新再提示', async () => {
    const confirm = answerConfirm(true)
    apiMock.mockResolvedValue({ message: '房间已复活' })
    const order: string[] = []
    const after = vi.fn(async () => {
      order.push('refresh')
    })
    const a = useSessionActions()
    expect(await a.reviveSession({ id: 3, name: '雾中' }, after)).toBe(true)
    expect(confirm).toHaveBeenCalledWith('确认复活房间「雾中」？', { confirmText: '复活' })
    expect(apiMock).toHaveBeenCalledWith('POST', '/api/sessions/3/revive')
    order.push(`toast:${ui.toasts.length}`)
    expect(order).toEqual(['refresh', 'toast:1'])
    expect(toasts()).toEqual([['房间已复活', 'success']])
    expect(a.reviving.value).toBe(false)
  })

  test('取消不发请求；失败提示错误', async () => {
    answerConfirm(false)
    expect(await useSessionActions().reviveSession({ id: 3 })).toBe(false)
    expect(apiMock).not.toHaveBeenCalled()

    answerConfirm(true)
    apiMock.mockRejectedValue(new ApiError('只有管理员可以复活房间', 403))
    expect(await useSessionActions().reviveSession({ id: 3 })).toBe(false)
    expect(toasts()).toEqual([['只有管理员可以复活房间', 'error']])
  })
})

describe('收藏', () => {
  test('收藏成功提示「已收藏」并刷新', async () => {
    apiMock.mockResolvedValue({ message: '已收藏' })
    const after = vi.fn().mockResolvedValue(undefined)
    expect(await useSessionActions().addFavorite(8, after)).toBe(true)
    expect(apiMock).toHaveBeenCalledWith('POST', '/api/sessions/8/favorite')
    expect(toasts()).toEqual([['已收藏', 'success']])
    expect(after).toHaveBeenCalledOnce()
  })

  test('取消收藏成功提示「已取消收藏」', async () => {
    apiMock.mockResolvedValue({ message: '已取消收藏' })
    expect(await useSessionActions().removeFavorite(8)).toBe(true)
    expect(apiMock).toHaveBeenCalledWith('DELETE', '/api/sessions/8/favorite')
    expect(toasts()).toEqual([['已取消收藏', 'success']])
  })

  test('失败时提示错误且不刷新', async () => {
    apiMock.mockRejectedValue(new ApiError('只有房间参与者可以收藏此房间', 403))
    const after = vi.fn()
    expect(await useSessionActions().addFavorite(8, after)).toBe(false)
    expect(toasts()).toEqual([['只有房间参与者可以收藏此房间', 'error']])
    expect(after).not.toHaveBeenCalled()
  })
})
