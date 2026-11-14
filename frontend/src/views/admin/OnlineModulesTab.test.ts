// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { BrowseItem, OnlineModule } from '@/api/adminTypesOnline'
import { useUiStore } from '@/stores/ui'
import { FakeWebSocket } from '@/testing/fakeWebSocket'
import OnlineModulesTab from './OnlineModulesTab.vue'

const mod = (over: Partial<OnlineModule> = {}): OnlineModule =>
  ({
    id: 7,
    source: 'cnmods',
    source_key: '1850',
    title: '雨夜档案',
    author: '某作者',
    share_url: '',
    meta: {},
    status: 'extracted',
    error: '',
    stage: '',
    stage_note: '',
    task_started_at: null,
    scenario_id: null,
    updated_at: '2026-11-14T08:00:00Z',
    ...over,
  }) as OnlineModule

const browseItem = (): BrowseItem =>
  ({
    keyId: '1850',
    title: '雨夜档案',
    article: '某作者',
    moduleType: 'COC',
    url: 'https://example.lanzoul.com/abc',
    opinion: '',
    local: null,
  }) as BrowseItem

let paths: string[] = []

async function mountTab() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/admin/online-tasks', name: 'admin-online-tasks', component: { template: '<div />' } },
      { path: '/admin/:tab?', name: 'admin', component: { template: '<div />' } },
    ],
  })
  await router.push('/admin/online')
  await router.isReady()
  const w = mount(OnlineModulesTab, { attachTo: document.body, global: { plugins: [router] } })
  await flushPromises()
  return { w, router }
}

const buttonByText = (text: string) =>
  Array.from(document.body.querySelectorAll('button')).find((b) => b.textContent?.trim() === text) as HTMLButtonElement | undefined
const listLoads = () => paths.filter((p) => p.startsWith('/api/admin/online-modules?page='))

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.setItem('coc_token', 't')
  document.body.innerHTML = ''
  paths = []
  FakeWebSocket.install()
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string) => {
      paths.push(path)
      const body = path.includes('/browse')
        ? { items: [browseItem()], page: 1, total_pages: 1, total_elements: 1 }
        : { items: [], page: 1, page_size: 20, total: 0, total_pages: 1 }
      return { ok: true, status: 200, json: async () => body } as Response
    }),
  )
})

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

describe('OnlineModulesTab', () => {
  test('挂载时打开任务连接，卸载时关闭', async () => {
    const { w } = await mountTab()
    expect(FakeWebSocket.instances).toHaveLength(1)
    expect(FakeWebSocket.last.closed).toBe(false)
    w.unmount()
    expect(FakeWebSocket.last.closed).toBe(true)
  })

  test('有「任务进度」入口，点击进入 /admin/online-tasks', async () => {
    const { w, router } = await mountTab()
    buttonByText('任务进度')!.click()
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('admin-online-tasks')
    w.unmount()
  })

  test('WebSocket 推来的状态同步到浏览区的行；任务结束时刷新本地列表，其余变化不刷新', async () => {
    const { w } = await mountTab()
    const first = FakeWebSocket.last
    first.recv({ type: 'snapshot', data: { modules: [mod({ status: 'downloading', stage: 'download' })] } })
    await flushPromises()
    expect(buttonByText('下载中…')!.disabled).toBe(true)
    expect(document.body.textContent).toContain('下载中')
    const loadsBefore = listLoads().length

    first.recv({ type: 'module', data: mod({ status: 'downloading', stage: 'ocr', stage_note: 'a.pdf' }) })
    await flushPromises()
    expect(listLoads()).toHaveLength(loadsBefore)

    first.recv({ type: 'module', data: mod({ status: 'extracted' }) })
    await flushPromises()
    expect(document.body.textContent).toContain('已提取')
    expect(buttonByText('下载')).toBeUndefined()
    expect(listLoads()).toHaveLength(loadsBefore + 1)
    expect(useUiStore().toasts.map((t) => [t.message, t.type])).toStrictEqual([['已提取正文：雨夜档案', 'success']])
    w.unmount()
  })

  test('任务失败：浏览区出现失败提示，toast 带失败原因；卸载后不再响应推送', async () => {
    const { w } = await mountTab()
    const ws = FakeWebSocket.last
    ws.recv({ type: 'snapshot', data: { modules: [mod({ status: 'downloading', stage: 'download' })] } })
    ws.recv({ type: 'module', data: mod({ status: 'failed', error: '下载被拦截' }) })
    await flushPromises()
    expect(document.body.querySelector('[role="alert"]')!.textContent).toContain('下载被拦截')
    expect(useUiStore().toasts.map((t) => [t.message, t.type])).toStrictEqual([['雨夜档案：下载被拦截', 'error']])

    w.unmount()
    const n = listLoads().length
    ws.recv({ type: 'module', data: mod({ status: 'extracted' }) })
    await flushPromises()
    expect(listLoads()).toHaveLength(n)
  })
})
