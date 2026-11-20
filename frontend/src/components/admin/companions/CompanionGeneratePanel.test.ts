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

function setup(reload: () => Promise<void> = async () => {}) {
  FakeWS.all = []
  vi.stubGlobal('WebSocket', FakeWS)
  const pinia = createPinia()
  setActivePinia(pinia)
  const wrapper = mount(CompanionGeneratePanel, { props: { reload }, global: { plugins: [pinia] } })
  return { wrapper, sockets: FakeWS.all }
}

const toasts = () => useUiStore().toasts.map((t) => [t.message, t.type])

describe('CompanionGeneratePanel', () => {
  test('默认生成 5 张：连接批量生成接口并发出 count', async () => {
    const { wrapper, sockets } = setup()
    expect(wrapper.find('button').text()).toBe('批量生成')

    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(sockets).toHaveLength(1)
    expect(sockets[0].url).toMatch(/^wss?:\/\/[^/]+\/api\/admin\/companion-cards\/generate\/ws\?token=t$/)
    expect(JSON.parse(sockets[0].sent[0])).toStrictEqual({ count: 5 })
    expect(wrapper.text()).toContain('已提交生成请求，等待服务器响应…')
    wrapper.unmount()
  })

  test('生成数量越界或不是整数：弹错误提示，不建立连接', async () => {
    const { wrapper, sockets } = setup()
    const input = wrapper.find('input[type="number"]')
    for (const bad of [0, 11, 2.5]) {
      await input.setValue(bad)
      await wrapper.find('button').trigger('click')
    }
    expect(sockets).toHaveLength(0)
    expect(toasts()).toHaveLength(3)
    expect(toasts()[0]).toStrictEqual(['生成数量必须是 1-10 之间的整数', 'error'])
    wrapper.unmount()
  })

  test('生成中按钮禁用且不能重复提交', async () => {
    const { wrapper, sockets } = setup()
    await wrapper.find('input[type="number"]').setValue(2)
    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(wrapper.find('button').attributes('disabled')).toBeDefined()
    await wrapper.find('button').trigger('click')
    expect(sockets).toHaveLength(1)
    wrapper.unmount()
  })

  test('收到事件后显示进度；batch_done 后弹成功提示并刷新卡池', async () => {
    const reload = vi.fn(async () => {})
    const { wrapper, sockets } = setup(reload)
    await wrapper.find('button').trigger('click')
    await flushPromises()

    sockets[0].recv('card_start', { index: 1, name: '陈守拙', occupation: '记者', gender: '男', age: 31 })
    sockets[0].recv('card_done', { index: 1, card_id: 9, name: '陈守拙', occupation: '记者' })
    sockets[0].recv('batch_done', { total: 1, succeeded: 1, failed: 0, results: [] })
    sockets[0].onclose?.()
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
    const { wrapper, sockets } = setup(reload)
    await wrapper.find('button').trigger('click')
    await flushPromises()

    sockets[0].recv('card_error', { index: 2, name: '苏闻笛', message: 'AI生成失败: 模型超时' })
    sockets[0].recv('batch_done', { total: 2, succeeded: 1, failed: 1, results: [] })
    sockets[0].onclose?.()
    await flushPromises()

    expect(wrapper.text()).toContain('第 2 张失败：苏闻笛，AI生成失败: 模型超时')
    expect(toasts()).toStrictEqual([['批量生成完成：成功 1 张，失败 1 张', 'error']])
    expect(reload).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  test('连接中断：提示后台仍在生成，不刷新卡池', async () => {
    const reload = vi.fn(async () => {})
    const { wrapper, sockets } = setup(reload)
    await wrapper.find('button').trigger('click')
    await flushPromises()

    sockets[0].onclose?.()
    await flushPromises()

    expect(toasts()).toStrictEqual([['连接中断，生成仍在后台继续', 'error']])
    expect(reload).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  test('服务端拒绝请求时弹出拒绝原因，按钮恢复可用', async () => {
    const { wrapper, sockets } = setup()
    await wrapper.find('button').trigger('click')
    await flushPromises()

    sockets[0].recv('rejected', { message: '已有批量生成任务在运行，请等它结束后再试' })
    sockets[0].onclose?.()
    await flushPromises()

    expect(toasts()).toStrictEqual([['已有批量生成任务在运行，请等它结束后再试', 'error']])
    expect(wrapper.find('button').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  test('卸载时关闭在途连接，且之后不再弹 toast', async () => {
    const { wrapper, sockets } = setup()
    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(sockets[0].closed).toBe(false)
    wrapper.unmount()
    expect(sockets[0].closed).toBe(true)
    await flushPromises()
    expect(toasts()).toStrictEqual([])
  })
})
