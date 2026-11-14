// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { OnlineModule } from '@/api/adminTypesOnline'
import { createAppRouter } from '@/router'
import { useOnlineTasksStore } from '@/stores/onlineTasks'
import { FakeWebSocket } from '@/testing/fakeWebSocket'
import OnlineTasksView from './OnlineTasksView.vue'

const NOW = '2026-11-14T09:02:05Z'

const mod = (id: number, over: Partial<OnlineModule> = {}): OnlineModule =>
  ({
    id,
    source: 'cnmods',
    source_key: String(1000 + id),
    title: '模组' + id,
    author: '作者' + id,
    share_url: '',
    meta: {},
    status: 'extracted',
    error: '',
    stage: '',
    stage_note: '',
    task_started_at: null,
    archive_name: '',
    archive_size: 0,
    archive_sha256: '',
    license_override_by: null,
    license_override_at: null,
    scenario_id: null,
    created_at: '2026-11-14T08:00:00Z',
    updated_at: '2026-11-14T08:00:00Z',
    ...over,
  }) as OnlineModule

const snapshot = (modules: OnlineModule[]) => FakeWebSocket.last.recv({ type: 'snapshot', data: { modules } })

function makeRouter() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/admin/online-tasks', name: 'admin-online-tasks', component: { template: '<div />' } },
      { path: '/admin/:tab?', name: 'admin', component: { template: '<div />' } },
    ],
  })
  return router
}

async function mountView() {
  const router = makeRouter()
  await router.push('/admin/online-tasks')
  await router.isReady()
  const w = mount(OnlineTasksView, { attachTo: document.body, global: { plugins: [router] } })
  await flushPromises()
  return { w, router }
}

const rowTitles = () => Array.from(document.body.querySelectorAll('[data-testid="task-row"] p.truncate')).map((p) => p.textContent)
const buttonByText = (text: string) =>
  Array.from(document.body.querySelectorAll('button')).find((b) => b.textContent?.trim() === text) as HTMLButtonElement | undefined

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.setItem('coc_token', 't')
  document.body.innerHTML = ''
  // NOTE: 只伪造 Date 和 setInterval：任务结束时 ui store 会弹 toast，里面有个 3.5 秒的 setTimeout，
  // 一起伪造会混进 getTimerCount，干扰「只有一个每秒计时器」的断言
  vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
  vi.setSystemTime(new Date(NOW))
  FakeWebSocket.install()
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string) => ({
      ok: true,
      status: 200,
      json: async () => (path.includes('/online-modules/7') ? { module: mod(7, { title: '雨夜档案' }), documents: [] } : {}),
    })),
  )
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  localStorage.clear()
})

describe('路由', () => {
  test('/admin/online-tasks 不会被 /admin/:tab? 吞掉，且要求管理员', () => {
    const router = createAppRouter(createMemoryHistory())
    const hit = router.resolve('/admin/online-tasks')
    expect(hit.name).toBe('admin-online-tasks')
    expect(hit.meta.admin).toBe(true)
    expect(router.resolve('/admin/online').name).toBe('admin')
    expect(router.resolve('/admin').name).toBe('admin')
  })
})

describe('OnlineTasksView', () => {
  test('挂载时打开连接，卸载时关闭；快照到达前显示骨架而不是空状态', async () => {
    const { w } = await mountView()
    expect(FakeWebSocket.instances).toHaveLength(1)
    expect(document.body.querySelector('[aria-busy="true"]')).not.toBeNull()
    expect(document.body.textContent).not.toContain('还没有任务记录')

    w.unmount()
    expect(FakeWebSocket.last.closed).toBe(true)
  })

  test('连接状态：快照前和断线后显示「重新连接中…」，快照到达后显示「已连接」', async () => {
    const { w } = await mountView()
    const indicator = () => document.body.querySelector('[data-testid="connection-indicator"]')!.textContent?.trim()
    expect(indicator()).toBe('重新连接中…')

    snapshot([])
    await flushPromises()
    expect(indicator()).toBe('已连接')

    FakeWebSocket.last.drop()
    await flushPromises()
    expect(indicator()).toBe('重新连接中…')
    w.unmount()
  })

  test('已加载且没有任何任务：显示空状态', async () => {
    const { w } = await mountView()
    snapshot([])
    await flushPromises()
    expect(document.body.textContent).toContain('还没有任务记录')
    expect(document.body.querySelector('[data-testid="task-row"]')).toBeNull()
    w.unmount()
  })

  test('排序：下载中在前（开始得晚的更靠前），其余按更新时间倒序；快照之后 module 帧也按同样规则重排', async () => {
    const { w } = await mountView()
    snapshot([
      mod(1, { updated_at: '2026-11-14T08:00:00Z' }),
      mod(2, { status: 'downloading', stage: 'download', task_started_at: '2026-11-14T09:00:00Z', updated_at: '2026-11-14T09:00:00Z' }),
      mod(3, { status: 'failed', error: 'x', updated_at: '2026-11-14T08:30:00Z' }),
      mod(4, { status: 'downloading', stage: 'ocr', task_started_at: '2026-11-14T09:01:00Z', updated_at: '2026-11-14T09:01:00Z' }),
    ])
    await flushPromises()
    expect(rowTitles()).toStrictEqual(['模组4', '模组2', '模组3', '模组1'])

    FakeWebSocket.last.recv({ type: 'module', data: mod(4, { status: 'extracted', updated_at: '2026-11-14T09:02:00Z' }) })
    await flushPromises()
    expect(rowTitles()).toStrictEqual(['模组2', '模组4', '模组3', '模组1'])

    FakeWebSocket.last.recv({ type: 'deleted', data: { id: 2 } })
    await flushPromises()
    expect(rowTitles()).toStrictEqual(['模组4', '模组3', '模组1'])
    w.unmount()
  })

  test('每行：标题、作者、状态徽标；下载中的行显示阶段、备注和已用时，并每秒递增', async () => {
    const { w } = await mountView()
    snapshot([
      mod(1, { status: 'downloading', stage: 'ocr', stage_note: 'scan.pdf', task_started_at: '2026-11-14T09:00:00Z' }),
      mod(2, { status: 'extracted' }),
    ])
    await flushPromises()

    const rows = document.body.querySelectorAll('[data-testid="task-row"]')
    expect(rows[0]!.textContent).toContain('模组1')
    expect(rows[0]!.textContent).toContain('作者1')
    expect(rows[0]!.textContent).toContain('下载中')
    const progress = rows[0]!.querySelector('[data-testid="task-progress"]')!
    expect(progress.textContent).toContain('OCR 识别')
    expect(progress.textContent).toContain('scan.pdf')
    expect(progress.textContent).toContain('2分05秒')
    // 非活动行没有进度
    expect(rows[1]!.querySelector('[data-testid="task-progress"]')).toBeNull()
    expect(rows[1]!.textContent).toContain('已提取')

    vi.advanceTimersByTime(1000)
    await flushPromises()
    expect(document.body.querySelector('[data-testid="task-progress"]')!.textContent).toContain('2分06秒')
    w.unmount()
  })

  test('阶段更新：module 帧到达后阶段文字变化', async () => {
    const { w } = await mountView()
    snapshot([mod(1, { status: 'downloading', stage: 'resolve', task_started_at: '2026-11-14T09:02:00Z' })])
    await flushPromises()
    expect(document.body.querySelector('[data-testid="task-progress"]')!.textContent).toContain('解析分享页')

    FakeWebSocket.last.recv({ type: 'module', data: mod(1, { status: 'downloading', stage: 'extract', task_started_at: '2026-11-14T09:02:00Z' }) })
    await flushPromises()
    expect(document.body.querySelector('[data-testid="task-progress"]')!.textContent).toContain('提取正文')
    w.unmount()
  })

  test('error 非空就显示失败文字（failed 和恢复后的 extracted 都显示），用 danger 样式；error 为空不显示', async () => {
    const { w } = await mountView()
    snapshot([
      mod(1, { status: 'failed', error: '下载被拦截' }),
      mod(2, { status: 'extracted', error: '压缩包损坏' }),
      mod(3, { status: 'extracted', error: '' }),
    ])
    await flushPromises()
    const errors = Array.from(document.body.querySelectorAll('[data-testid="task-error"]'))
    expect(errors.map((e) => e.textContent)).toStrictEqual(expect.arrayContaining(['下载被拦截', '压缩包损坏']))
    expect(errors).toHaveLength(2)
    for (const e of errors) expect(e.className).toContain('text-danger')
    w.unmount()
  })

  test('已编译的模组显示「已编译」徽标', async () => {
    const { w } = await mountView()
    snapshot([mod(1, { scenario_id: 5 })])
    await flushPromises()
    expect(document.body.querySelector('[data-testid="task-row"]')!.textContent).toContain('已编译')
    w.unmount()
  })

  test('没有活动任务时不开计时器；任务出现后开一个；任务结束或卸载后清掉', async () => {
    const { w } = await mountView()
    snapshot([mod(1)])
    await flushPromises()
    expect(vi.getTimerCount()).toBe(0)

    FakeWebSocket.last.recv({ type: 'module', data: mod(2, { status: 'downloading', stage: 'download', task_started_at: NOW }) })
    FakeWebSocket.last.recv({ type: 'module', data: mod(3, { status: 'downloading', stage: 'download', task_started_at: NOW }) })
    await flushPromises()
    expect(vi.getTimerCount()).toBe(1)

    FakeWebSocket.last.recv({ type: 'module', data: mod(2) })
    await flushPromises()
    expect(vi.getTimerCount()).toBe(1)
    FakeWebSocket.last.recv({ type: 'module', data: mod(3) })
    await flushPromises()
    expect(vi.getTimerCount()).toBe(0)

    FakeWebSocket.last.recv({ type: 'module', data: mod(4, { status: 'downloading', stage: 'download', task_started_at: NOW }) })
    await flushPromises()
    expect(vi.getTimerCount()).toBe(1)
    w.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })

  test('「查看」打开该模组的详情弹窗（请求 /api/admin/online-modules/:id）', async () => {
    const { w } = await mountView()
    snapshot([mod(7, { title: '雨夜档案' })])
    await flushPromises()
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()

    buttonByText('查看')!.click()
    await flushPromises()
    const fetchFn = globalThis.fetch as unknown as ReturnType<typeof vi.fn>
    expect(fetchFn.mock.calls.map((c) => c[0])).toContain('/api/admin/online-modules/7')
    expect(document.body.querySelector('[role="dialog"]')).not.toBeNull()
    expect(document.body.querySelector('[role="dialog"]')!.textContent).toContain('雨夜档案')
    w.unmount()
  })

  test('返回按钮回到 /admin/online', async () => {
    const { w, router } = await mountView()
    buttonByText('返回线上模组')!.click()
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/admin/online')
    w.unmount()
  })
})
