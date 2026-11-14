// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { useUiStore } from '@/stores/ui'
import ScenarioGeneratePanel from './ScenarioGeneratePanel.vue'
import StoryCompilePanel from './StoryCompilePanel.vue'

beforeEach(() => {
  localStorage.setItem('coc_token', 't')
})

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

// NOTE: 假 WebSocket：连接打开后先推一条 progress，之后一直挂着，直到面板主动 close。
// 真实浏览器里 open 之前客户端不能 send，这里用 flushPromises 后的微任务模拟握手完成
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
    queueMicrotask(() => {
      this.onopen?.()
      this.recv('progress', { stage: 'queued', status: 'start', detail: '排队中' })
    })
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

function stubHangingSocket() {
  FakeWS.all = []
  vi.stubGlobal('WebSocket', FakeWS)
  return FakeWS.all
}

describe('ScenarioGeneratePanel', () => {
  test('最大玩家数小于最小玩家数：弹错误提示，不建立连接', async () => {
    const sockets = stubHangingSocket()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(ScenarioGeneratePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] } })
    const numbers = wrapper.findAll('input[type="number"]')
    await numbers[0].setValue(5)
    await numbers[1].setValue(2)
    await wrapper.find('button').trigger('click')
    expect(sockets).toHaveLength(0)
    expect(useUiStore().toasts.map((t) => [t.message, t.type])).toStrictEqual([['最大玩家数不能小于最小玩家数', 'error']])
  })

  test('提交后显示进度日志；卸载时关闭在途连接，且之后不再弹 toast', async () => {
    const sockets = stubHangingSocket()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(ScenarioGeneratePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] } })

    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(sockets).toHaveLength(1)
    expect(sockets[0].url).toMatch(/^wss?:\/\/[^/]+\/api\/scenarios\/generate\/ws\?token=t$/)
    expect(sockets[0].sent).toHaveLength(1)
    expect(JSON.parse(sockets[0].sent[0])).toStrictEqual({
      name: '',
      theme: '',
      era: '',
      brief: '',
      target_length: 'short',
      min_players: 1,
      max_players: 4,
      difficulty: 'normal',
      count: 1,
    })
    const text = wrapper.text()
    expect(text).toContain('已提交生成请求，等待服务器响应…')
    expect(text).toContain('排队中')
    expect(text).toContain('后台有 1 个生成任务运行/排队中')

    expect(sockets[0].closed).toBe(false)
    wrapper.unmount()
    expect(sockets[0].closed).toBe(true)
    await flushPromises()
    expect(useUiStore().toasts).toStrictEqual([])
  })

  test('批量数量大于 1 时按钮文案变化，并把 count 带给后端', async () => {
    const sockets = stubHangingSocket()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(ScenarioGeneratePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] } })
    // NOTE: 数字输入框依次是最小玩家数、最大玩家数、生成数量（难度是下拉框）
    await wrapper.findAll('input[type="number"]')[2].setValue(3)
    expect(wrapper.find('button').text()).toBe('批量生成 3 个并入库')
    await wrapper.find('button').trigger('click')
    await flushPromises()
    expect(JSON.parse(sockets[0].sent[0]).count).toBe(3)
    wrapper.unmount()
  })

  test('收到 batch_done 后弹成功提示并刷新列表', async () => {
    const sockets = stubHangingSocket()
    const reload = vi.fn(async () => {})
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(ScenarioGeneratePanel, { props: { reload }, global: { plugins: [pinia] } })
    await wrapper.find('button').trigger('click')
    await flushPromises()

    sockets[0].recv('done', { index: 1, scenario_id: 9, name: '雨夜档案' })
    sockets[0].recv('batch_done', { total: 1, succeeded: 1, failed: 0, results: [{ index: 1, scenario_id: 9, name: '雨夜档案' }] })
    sockets[0].onclose?.()
    await flushPromises()

    expect(wrapper.text()).toContain('第 1 个生成完成：《雨夜档案》，已入库')
    expect(useUiStore().toasts.map((t) => [t.message, t.type])).toStrictEqual([['AI 模组生成并入库成功', 'success']])
    expect(reload).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  test('服务端拒绝请求时弹出拒绝原因', async () => {
    const sockets = stubHangingSocket()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(ScenarioGeneratePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] } })
    await wrapper.find('button').trigger('click')
    await flushPromises()

    sockets[0].recv('rejected', { message: '生成数量不能超过 10' })
    sockets[0].onclose?.()
    await flushPromises()

    expect(useUiStore().toasts.map((t) => [t.message, t.type])).toStrictEqual([['生成数量不能超过 10', 'error']])
    wrapper.unmount()
  })
})

describe('StoryCompilePanel', () => {
  test('未选文件：提示选择文件，不建立连接', async () => {
    const sockets = stubHangingSocket()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(StoryCompilePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] } })
    await wrapper.find('button').trigger('click')
    expect(sockets).toHaveLength(0)
    expect(useUiStore().toasts.map((t) => t.message)).toStrictEqual(['请选择一个 .md 文件'])
  })

  test('选了文件后开始编译；卸载时关闭在途连接', async () => {
    const sockets = stubHangingSocket()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(StoryCompilePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] }, attachTo: document.body })
    const input = wrapper.find('input[type="file"]').element as HTMLInputElement
    const file = new File(['# 雨夜\n故事正文'], 'story.md', { type: 'text/markdown' })
    Object.defineProperty(input, 'files', { value: [file], configurable: true })

    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(sockets).toHaveLength(1)
    expect(sockets[0].url).toMatch(/^wss?:\/\/[^/]+\/api\/scenarios\/compile-story\/ws\?token=t$/)
    expect(JSON.parse(sockets[0].sent[0])).toStrictEqual({ story_document: '# 雨夜\n故事正文', name: '' })
    expect(wrapper.text()).toContain('已提交编译请求，等待服务器响应…')
    expect(wrapper.find('button').text()).toBe('编译中…')

    wrapper.unmount()
    expect(sockets[0].closed).toBe(true)
    await flushPromises()
    expect(useUiStore().toasts).toStrictEqual([])
  })

  test('收到 done 后弹成功提示并刷新列表', async () => {
    const sockets = stubHangingSocket()
    const reload = vi.fn(async () => {})
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(StoryCompilePanel, { props: { reload }, global: { plugins: [pinia] }, attachTo: document.body })
    const input = wrapper.find('input[type="file"]').element as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [new File(['正文'], 'story.md')], configurable: true })
    await wrapper.find('button').trigger('click')
    await flushPromises()

    sockets[0].recv('done', { index: 1, scenario_id: 42, name: '雨夜档案' })
    sockets[0].onclose?.()
    await flushPromises()

    expect(useUiStore().toasts.map((t) => [t.message, t.type])).toStrictEqual([['故事编译并入库成功', 'success']])
    expect(reload).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  test('扩展名不合法：提示并且不建立连接', async () => {
    const sockets = stubHangingSocket()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(StoryCompilePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] } })
    const input = wrapper.find('input[type="file"]').element as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [new File(['x'], 'a.docx')], configurable: true })
    await wrapper.find('button').trigger('click')
    expect(sockets).toHaveLength(0)
    expect(useUiStore().toasts.map((t) => t.message)).toStrictEqual(['仅支持 .md / .markdown / .txt 格式'])
  })
})
