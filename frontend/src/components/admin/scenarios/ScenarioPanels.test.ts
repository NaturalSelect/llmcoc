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

// NOTE: 模拟一个永不结束的 SSE 流：先吐一条 progress，收到 abort 后像真实 fetch 一样让 reader.read() 以 AbortError 失败
function stubHangingStream() {
  const signals: AbortSignal[] = []
  const enc = new TextEncoder()
  const fn = vi.fn(async (_url: string, init: RequestInit) => {
    const signal = init.signal as AbortSignal
    signals.push(signal)
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        c.enqueue(enc.encode('event: progress\ndata: {"stage":"queued","status":"start","detail":"排队中"}\n\n'))
        signal.addEventListener('abort', () => c.error(new DOMException('aborted', 'AbortError')))
      },
    })
    return { ok: true, status: 200, body } as unknown as Response
  })
  vi.stubGlobal('fetch', fn)
  return { fn, signals }
}

describe('ScenarioGeneratePanel', () => {
  test('最大玩家数小于最小玩家数：弹错误提示，不发请求', async () => {
    const { fn } = stubHangingStream()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(ScenarioGeneratePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] } })
    const numbers = wrapper.findAll('input[type="number"]')
    await numbers[0].setValue(5)
    await numbers[1].setValue(2)
    await wrapper.find('button').trigger('click')
    expect(fn).not.toHaveBeenCalled()
    expect(useUiStore().toasts.map((t) => [t.message, t.type])).toStrictEqual([['最大玩家数不能小于最小玩家数', 'error']])
  })

  test('提交后显示进度日志；卸载时中断在途请求，且之后不再弹 toast', async () => {
    const { fn, signals } = stubHangingStream()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(ScenarioGeneratePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] } })

    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(fn).toHaveBeenCalledTimes(1)
    const [path, init] = fn.mock.calls[0] as unknown as [string, RequestInit]
    expect(path).toBe('/api/scenarios/generate')
    expect(JSON.parse(init.body as string)).toStrictEqual({
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

    expect(signals[0].aborted).toBe(false)
    wrapper.unmount()
    expect(signals[0].aborted).toBe(true)
    await flushPromises()
    expect(useUiStore().toasts).toStrictEqual([])
  })

  test('批量数量大于 1 时按钮文案变化，并把 count 带给后端', async () => {
    const { fn } = stubHangingStream()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(ScenarioGeneratePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] } })
    // NOTE: 数字输入框依次是最小玩家数、最大玩家数、生成数量（难度是下拉框）
    await wrapper.findAll('input[type="number"]')[2].setValue(3)
    expect(wrapper.find('button').text()).toBe('批量生成 3 个并入库')
    await wrapper.find('button').trigger('click')
    await flushPromises()
    expect(JSON.parse((fn.mock.calls[0] as unknown as [string, RequestInit])[1].body as string).count).toBe(3)
    wrapper.unmount()
  })
})

describe('StoryCompilePanel', () => {
  test('未选文件：提示选择文件，不发请求', async () => {
    const { fn } = stubHangingStream()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(StoryCompilePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] } })
    await wrapper.find('button').trigger('click')
    expect(fn).not.toHaveBeenCalled()
    expect(useUiStore().toasts.map((t) => t.message)).toStrictEqual(['请选择一个 .md 文件'])
  })

  test('选了文件后开始编译；卸载时中断在途请求', async () => {
    const { fn, signals } = stubHangingStream()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(StoryCompilePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] }, attachTo: document.body })
    const input = wrapper.find('input[type="file"]').element as HTMLInputElement
    const file = new File(['# 雨夜\n故事正文'], 'story.md', { type: 'text/markdown' })
    Object.defineProperty(input, 'files', { value: [file], configurable: true })

    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(fn).toHaveBeenCalledTimes(1)
    const [path, init] = fn.mock.calls[0] as unknown as [string, RequestInit]
    expect(path).toBe('/api/scenarios/compile-story')
    expect(JSON.parse(init.body as string)).toStrictEqual({ story_document: '# 雨夜\n故事正文', name: '' })
    expect(wrapper.text()).toContain('已提交编译请求，等待服务器响应…')
    expect(wrapper.find('button').text()).toBe('编译中…')

    wrapper.unmount()
    expect(signals[0].aborted).toBe(true)
    await flushPromises()
    expect(useUiStore().toasts).toStrictEqual([])
  })

  test('扩展名不合法：提示并且不发请求', async () => {
    const { fn } = stubHangingStream()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = mount(StoryCompilePanel, { props: { reload: async () => {} }, global: { plugins: [pinia] } })
    const input = wrapper.find('input[type="file"]').element as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [new File(['x'], 'a.docx')], configurable: true })
    await wrapper.find('button').trigger('click')
    expect(fn).not.toHaveBeenCalled()
    expect(useUiStore().toasts.map((t) => t.message)).toStrictEqual(['仅支持 .md / .markdown / .txt 格式'])
  })
})
