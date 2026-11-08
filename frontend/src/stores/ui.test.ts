// @vitest-environment jsdom
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { useUiStore } from './ui'

beforeEach(() => {
  setActivePinia(createPinia())
  vi.useFakeTimers()
})

describe('ui store', () => {
  test('toast 到时自动消失，多个 toast 互不影响', () => {
    const ui = useUiStore()
    ui.toast('第一条')
    vi.advanceTimersByTime(2000)
    ui.toast('出错了', 'error')
    expect(ui.toasts.map((t) => [t.message, t.type])).toStrictEqual([
      ['第一条', 'success'],
      ['出错了', 'error'],
    ])
    vi.advanceTimersByTime(1500)
    expect(ui.toasts.map((t) => t.message)).toStrictEqual(['出错了'])
    vi.advanceTimersByTime(2000)
    expect(ui.toasts).toStrictEqual([])
  })

  test('confirm：确认返回 true，取消返回 false，关闭后状态清空', async () => {
    const ui = useUiStore()
    const yes = ui.confirm('删除？', { danger: true, confirmText: '删除' })
    expect(ui.confirmState).toMatchObject({ message: '删除？', danger: true, confirmText: '删除', withInput: false })
    ui.resolveConfirm(true)
    expect(await yes).toBe(true)
    expect(ui.confirmState).toBeNull()

    const no = ui.confirm('继续？')
    expect(ui.confirmState?.confirmText).toBe('确认')
    ui.resolveConfirm(false)
    expect(await no).toBe(false)
  })

  test('confirm withInput：确认返回输入内容，取消返回 null', async () => {
    const ui = useUiStore()
    const ok = ui.confirm('原因？', { withInput: true, inputPlaceholder: '请输入' })
    expect(ui.confirmState?.inputPlaceholder).toBe('请输入')
    ui.confirmState!.inputValue = '测试'
    ui.resolveConfirm(true)
    expect(await ok).toBe('测试')

    const cancel = ui.confirm('原因？', { withInput: true })
    ui.confirmState!.inputValue = '不会被返回'
    ui.resolveConfirm(false)
    expect(await cancel).toBeNull()
  })

  test('confirm：已有确认框时再次调用，上一个按取消结算，不会悬挂', async () => {
    const ui = useUiStore()
    const first = ui.confirm('第一个')
    const second = ui.confirm('第二个')
    expect(await first).toBe(false)
    expect(ui.confirmState?.message).toBe('第二个')
    ui.resolveConfirm(true)
    expect(await second).toBe(true)
  })

  test('没有确认框时 resolveConfirm 不报错', () => {
    expect(() => useUiStore().resolveConfirm(true)).not.toThrow()
  })
})
