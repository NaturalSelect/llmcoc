// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { useUiStore } from '@/stores/ui'
import ScenarioCreateCard from './ScenarioCreateCard.vue'

beforeEach(() => {
  localStorage.setItem('coc_token', 't')
})

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

// NOTE: 与 ScenarioPanels.test.ts 相同的假 WebSocket：打开后推一条 progress，之后一直挂着
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
      this.onmessage?.({ data: JSON.stringify({ type: 'progress', data: { stage: 'queued', status: 'start', detail: '排队中' } }) } as MessageEvent)
    })
  }
  send(data: string) {
    this.sent.push(data)
  }
  close() {
    this.closed = true
  }
}

function mountCard(reload: () => Promise<void> = async () => {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  return mount(ScenarioCreateCard, { props: { reload }, global: { plugins: [pinia] }, attachTo: document.body })
}

function tabButton(wrapper: ReturnType<typeof mountCard>, label: string) {
  const tab = wrapper.findAll('[role="tab"]').find((t) => t.text() === label)
  if (!tab) throw new Error('找不到页签：' + label)
  return tab
}

describe('ScenarioCreateCard', () => {
  test('三个页签，默认显示 AI 生成，其余面板已挂载但隐藏', () => {
    const wrapper = mountCard()
    expect(wrapper.findAll('[role="tab"]').map((t) => t.text())).toStrictEqual(['AI 生成', '故事编译', '导入 JSON'])
    expect(tabButton(wrapper, 'AI 生成').attributes('aria-selected')).toBe('true')
    // NOTE: v-show 只是加 display:none，三个面板都还在 DOM 里，卸载会中断 WebSocket
    expect(wrapper.findAll('input[type="file"]')).toHaveLength(2)
    const hidden = wrapper.findAll('div').filter((d) => (d.element as HTMLElement).style.display === 'none')
    expect(hidden).toHaveLength(2)
    wrapper.unmount()
  })

  test('生成中切到其他页签：连接不被关闭，切回来日志还在', async () => {
    FakeWS.all = []
    vi.stubGlobal('WebSocket', FakeWS)
    const wrapper = mountCard()

    await wrapper.find('button:not([role="tab"])').trigger('click')
    await flushPromises()
    expect(FakeWS.all).toHaveLength(1)
    expect(wrapper.text()).toContain('排队中')

    await tabButton(wrapper, '导入 JSON').trigger('click')
    expect(FakeWS.all[0].closed).toBe(false)

    await tabButton(wrapper, 'AI 生成').trigger('click')
    expect(FakeWS.all[0].closed).toBe(false)
    expect(wrapper.text()).toContain('排队中')

    wrapper.unmount()
    expect(FakeWS.all[0].closed).toBe(true)
  })

  test('导入页签：选文件后上传，成功提示并刷新列表', async () => {
    const calls: Array<{ url: string; method?: string }> = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init: RequestInit = {}) => {
        calls.push({ url, method: init.method })
        return { ok: true, status: 200, json: async () => ({}) } as Response
      }),
    )
    const reload = vi.fn(async () => {})
    const wrapper = mountCard(reload)
    await tabButton(wrapper, '导入 JSON').trigger('click')

    const input = wrapper.find('input[aria-label="选择模组 JSON 文件"]')
    const uploadBtn = wrapper.findAll('button').find((b) => b.text() === '上传并导入')!
    expect(uploadBtn.attributes('disabled')).toBeDefined()

    Object.defineProperty(input.element, 'files', { value: [new File(['{}'], 'm.json', { type: 'application/json' })], configurable: true })
    await input.trigger('change')
    expect(uploadBtn.attributes('disabled')).toBeUndefined()

    await uploadBtn.trigger('click')
    await flushPromises()

    expect(calls).toStrictEqual([{ url: '/api/scenarios/upload', method: 'POST' }])
    expect(useUiStore().toasts.map((t) => [t.message, t.type])).toStrictEqual([['模组导入成功', 'success']])
    expect(reload).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
