// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { useUiStore } from '@/stores/ui'
import CompanionGeneratePanel from './CompanionGeneratePanel.vue'

beforeEach(() => {
  localStorage.setItem('coc_token', 't')
})

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

// NOTE: 假 WebSocket：连接打开后什么都不推，由用例按需 recv；真实浏览器里 open 之前不能 send，这里用微任务模拟握手完成
class FakeWS {
  static all: FakeWS[] = []
  onopen: (() => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  sent: string[] = []
  closed = false
  constructor(readonly url: string) {
    FakeWS.all.push(this)
    queueMicrotask(() => this.onopen?.())
  }
  send(data: string) {
    this.sent.push(data)
  }
  close() {
    this.closed = true
  }
  recv(type: string, data: unknown) {
    this.onmessage?.({ data: JSON.stringify({ type, data }) } as MessageEvent)
  }
}

// NOTE: 挂载时面板会先发一个 attach 连接（sockets[0]）；启动任务的连接是后面的 sockets[1]
function setup(reload: () => Promise<void> = async () => {}) {
  FakeWS.all = []
  vi.stubGlobal('WebSocket', FakeWS)
  const pinia = createPinia()
  setActivePinia(pinia)
  const wrapper = mount(CompanionGeneratePanel, { props: { reload }, global: { plugins: [pinia] } })
  return { wrapper, sockets: FakeWS.all }
}

// 服务端对空闲状态的应答：一帧空快照后关闭连接
async function setupIdle(reload?: () => Promise<void>) {
  const ctx = setup(reload)
  await flushPromises()
  ctx.sockets[0]!.recv('snapshot', { running: false, events: [] })
  ctx.sockets[0]!.onclose?.()
  await flushPromises()
  return ctx
}

const toasts = () => useUiStore().toasts.map((t) => [t.message, t.type])

const startEvent = { type: 'card_start', data: { index: 1, name: '陈守拙', occupation: '记者', gender: '男', age: 31 }, at: Date.UTC(2026, 10, 21, 8, 5, 3) }
const doneEvent = { type: 'card_done', data: { index: 1, card_id: 9, name: '陈守拙', occupation: '记者' }, at: Date.UTC(2026, 10, 21, 8, 9, 40) }
const batchEvent = { type: 'batch_done', data: { total: 1, succeeded: 1, failed: 0, results: [] }, at: Date.UTC(2026, 10, 21, 8, 9, 41) }

describe('CompanionGeneratePanel 启动任务', () => {
  test('默认生成 5 张：连接批量生成接口并发出 count', async () => {
    const { wrapper, sockets } = await setupIdle()
    expect(wrapper.find('button').text()).toBe('批量生成')

    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(sockets).toHaveLength(2)
    expect(sockets[1]!.url).toMatch(/^wss?:\/\/[^/]+\/api\/admin\/companion-cards\/generate\/ws\?token=t$/)
    expect(JSON.parse(sockets[1]!.sent[0]!)).toStrictEqual({ count: 5 })
    expect(wrapper.text()).toContain('已提交生成请求，等待服务器响应…')
    wrapper.unmount()
  })

  test('生成数量越界或不是整数：弹错误提示，不建立新连接', async () => {
    const { wrapper, sockets } = await setupIdle()
    const input = wrapper.find('input[type="number"]')
    for (const bad of [0, 11, 2.5]) {
      await input.setValue(bad)
      await wrapper.find('button').trigger('click')
    }
    expect(sockets).toHaveLength(1)
    expect(toasts()).toHaveLength(3)
    expect(toasts()[0]).toStrictEqual(['生成数量必须是 1-10 之间的整数', 'error'])
    wrapper.unmount()
  })

  test('生成中按钮禁用且不能重复提交', async () => {
    const { wrapper, sockets } = await setupIdle()
    await wrapper.find('input[type="number"]').setValue(2)
    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(wrapper.find('button').attributes('disabled')).toBeDefined()
    await wrapper.find('button').trigger('click')
    expect(sockets).toHaveLength(2)
    wrapper.unmount()
  })

  test('收到事件后显示进度；batch_done 后弹成功提示并刷新卡池', async () => {
    const reload = vi.fn(async () => {})
    const { wrapper, sockets } = await setupIdle(reload)
    await wrapper.find('button').trigger('click')
    await flushPromises()

    sockets[1]!.recv('card_start', startEvent.data)
    sockets[1]!.recv('card_done', doneEvent.data)
    sockets[1]!.recv('batch_done', batchEvent.data)
    sockets[1]!.onclose?.()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('第 1 张开始生成：陈守拙（男，31 岁，记者）')
    expect(text).toContain('第 1 张完成：陈守拙（记者），已放入卡池')
    expect(toasts()).toStrictEqual([['批量生成完成：成功 1 张，失败 0 张', 'success']])
    expect(reload).toHaveBeenCalledTimes(1)
    expect(wrapper.find('button').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  test('有失败的卡：错误提示并记录失败原因', async () => {
    const reload = vi.fn(async () => {})
    const { wrapper, sockets } = await setupIdle(reload)
    await wrapper.find('button').trigger('click')
    await flushPromises()

    sockets[1]!.recv('card_error', { index: 2, name: '苏闻笛', message: 'AI生成失败: 模型超时' })
    sockets[1]!.recv('batch_done', { total: 2, succeeded: 1, failed: 1, results: [] })
    sockets[1]!.onclose?.()
    await flushPromises()

    expect(wrapper.text()).toContain('第 2 张失败：苏闻笛，AI生成失败: 模型超时')
    expect(toasts()).toStrictEqual([['批量生成完成：成功 1 张，失败 1 张', 'error']])
    expect(reload).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  test('连接中断：提示后台仍在生成，不刷新卡池', async () => {
    const reload = vi.fn(async () => {})
    const { wrapper, sockets } = await setupIdle(reload)
    await wrapper.find('button').trigger('click')
    await flushPromises()

    sockets[1]!.onclose?.()
    await flushPromises()

    expect(toasts()).toStrictEqual([['连接中断，生成仍在后台继续', 'error']])
    expect(reload).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  test('服务端拒绝请求时弹出拒绝原因，按钮恢复可用', async () => {
    const { wrapper, sockets } = await setupIdle()
    await wrapper.find('button').trigger('click')
    await flushPromises()

    sockets[1]!.recv('rejected', { message: '已有批量生成任务在运行，请等它结束后再试' })
    sockets[1]!.onclose?.()
    await flushPromises()

    expect(toasts()).toStrictEqual([['已有批量生成任务在运行，请等它结束后再试', 'error']])
    expect(wrapper.find('button').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  test('卸载时关闭在途连接，且之后不再弹 toast', async () => {
    const { wrapper, sockets } = await setupIdle()
    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(sockets[1]!.closed).toBe(false)
    wrapper.unmount()
    expect(sockets[1]!.closed).toBe(true)
    await flushPromises()
    expect(toasts()).toStrictEqual([])
  })
})

describe('CompanionGeneratePanel 刷新后接上任务', () => {
  test('挂载时发 attach 请求，空闲时没有进度框也没有提示', async () => {
    const { wrapper, sockets } = setup()
    await flushPromises()

    expect(sockets).toHaveLength(1)
    expect(sockets[0]!.url).toMatch(/^wss?:\/\/[^/]+\/api\/admin\/companion-cards\/generate\/ws\?token=t$/)
    expect(JSON.parse(sockets[0]!.sent[0]!)).toStrictEqual({ attach: true })

    sockets[0]!.recv('snapshot', { running: false, events: [] })
    sockets[0]!.onclose?.()
    await flushPromises()

    expect(wrapper.text()).not.toContain('生成进度')
    expect(toasts()).toStrictEqual([])
    expect(wrapper.find('button').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  test('任务还在跑：回放已有进度（带原时间），按钮置为生成中，之后的事件继续追加，结束后提示并刷新卡池', async () => {
    const reload = vi.fn(async () => {})
    const { wrapper, sockets } = setup(reload)
    await flushPromises()

    sockets[0]!.recv('snapshot', { running: true, events: [startEvent] })
    await flushPromises()

    const startedAt = new Date(startEvent.at).toLocaleTimeString('zh-CN', { hour12: false })
    expect(wrapper.text()).toContain(`[${startedAt}] 第 1 张开始生成：陈守拙（男，31 岁，记者）`)
    expect(wrapper.find('button').attributes('disabled')).toBeDefined()
    expect(sockets).toHaveLength(1)

    sockets[0]!.recv('card_done', doneEvent.data)
    sockets[0]!.recv('batch_done', batchEvent.data)
    sockets[0]!.onclose?.()
    await flushPromises()

    expect(wrapper.text()).toContain('第 1 张完成：陈守拙（记者），已放入卡池')
    expect(toasts()).toStrictEqual([['批量生成完成：成功 1 张，失败 0 张', 'success']])
    expect(reload).toHaveBeenCalledTimes(1)
    expect(wrapper.find('button').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  test('任务已经结束：只回放上一批的记录，不弹提示也不刷新', async () => {
    const reload = vi.fn(async () => {})
    const { wrapper, sockets } = setup(reload)
    await flushPromises()

    sockets[0]!.recv('snapshot', { running: false, events: [startEvent, doneEvent, batchEvent] })
    sockets[0]!.onclose?.()
    await flushPromises()

    expect(wrapper.text()).toContain('第 1 张开始生成：陈守拙（男，31 岁，记者）')
    expect(wrapper.text()).toContain('第 1 张完成：陈守拙（记者），已放入卡池')
    expect(toasts()).toStrictEqual([])
    expect(reload).not.toHaveBeenCalled()
    expect(wrapper.find('button').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  test('任务在跑但连接中途断开：提示后台仍在生成', async () => {
    const { wrapper, sockets } = setup()
    await flushPromises()

    sockets[0]!.recv('snapshot', { running: true, events: [startEvent] })
    sockets[0]!.onclose?.()
    await flushPromises()

    expect(toasts()).toStrictEqual([['连接中断，生成仍在后台继续', 'error']])
    expect(wrapper.find('button').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  test('接不上时不打扰用户，按钮保持可用', async () => {
    const { wrapper, sockets } = setup()
    await flushPromises()

    sockets[0]!.recv('rejected', { message: '请求必须是 JSON 文本帧' })
    sockets[0]!.onclose?.()
    await flushPromises()

    expect(toasts()).toStrictEqual([])
    expect(wrapper.find('button').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  test('接上前就点了生成：旧连接被关闭，迟到的快照不会盖掉这次启动', async () => {
    const { wrapper, sockets } = setup()
    await flushPromises()

    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(sockets[0]!.closed).toBe(true)
    expect(sockets).toHaveLength(2)

    sockets[0]!.recv('snapshot', { running: false, events: [startEvent, doneEvent, batchEvent] })
    sockets[0]!.onclose?.()
    await flushPromises()

    expect(wrapper.text()).toContain('已提交生成请求，等待服务器响应…')
    expect(wrapper.text()).not.toContain('已放入卡池')
    expect(wrapper.find('button').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  test('卸载时关闭接上的连接', async () => {
    const { wrapper, sockets } = setup()
    await flushPromises()

    wrapper.unmount()
    expect(sockets[0]!.closed).toBe(true)
  })
})
