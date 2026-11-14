// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import type { BrowseItem, OnlineModule, OnlineModuleDetail, OnlineModuleDocument } from '@/api/adminTypesOnline'
import { useOnlineTasksStore } from '@/stores/onlineTasks'
import { useUiStore } from '@/stores/ui'
import { FakeWebSocket } from '@/testing/fakeWebSocket'
import OnlineBrowsePanel from './OnlineBrowsePanel.vue'
import OnlineModuleDetailModal from './OnlineModuleDetailModal.vue'

interface Call {
  method: string
  path: string
  body: unknown
}
let calls: Call[] = []
let routes: Record<string, unknown | (() => unknown)> = {}

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ method, path, body: typeof init?.body === 'string' ? JSON.parse(init.body) : init?.body })
      const key = `${method} ${path}`
      const raw = key in routes ? routes[key] : routes[`${method} ${path.split('?')[0]}`]
      if (raw === undefined) return { ok: false, status: 404, json: async () => ({ error: '无路由 ' + key }) }
      const hit = typeof raw === 'function' ? (raw as () => unknown)() : raw
      return { ok: true, status: 200, json: async () => hit }
    }),
  )
}

const module_ = (over: Partial<OnlineModule> = {}): OnlineModule => ({
  id: 7,
  source: 'cnmods',
  source_key: '1850',
  title: '雨夜档案',
  author: '某作者',
  share_url: 'https://example.lanzoul.com/abc',
  meta: { module_age: '现代', min_amount: 2, max_amount: 4, opinion: '<p>好玩&nbsp;的团</p>' },
  status: 'extracted',
  error: '',
  stage: '',
  stage_note: '',
  task_started_at: null,
  archive_name: 'a.zip',
  archive_size: 2048,
  archive_sha256: 'deadbeef',
  license_override_by: null,
  license_override_at: null,
  scenario_id: null,
  created_at: '2026-11-14T08:00:00Z',
  updated_at: '2026-11-14T08:00:00Z',
  ...over,
})

const doc = (over: Partial<OnlineModuleDocument> = {}): OnlineModuleDocument => ({
  id: 11,
  online_module_id: 7,
  path: '雨夜档案.docx',
  kind: 'docx',
  runes: 5000,
  candidate: true,
  ocr: false,
  garbled: false,
  license_hits: [],
  preview: '这是正文的前三百字预览',
  ...over,
})

const detail = (m: Partial<OnlineModule> = {}, docs: OnlineModuleDocument[] = [doc()]): OnlineModuleDetail => ({
  module: module_(m),
  documents: docs,
})

const browseItem = (over: Partial<BrowseItem> = {}): BrowseItem => ({
  keyId: '1850',
  title: '雨夜档案',
  article: '某作者',
  moduleType: 'COC',
  moduleAge: '现代',
  structure: '线性',
  minAmount: 2,
  maxAmount: 4,
  minDuration: 3,
  maxDuration: 4,
  freeLevel: '',
  releaseDate: '2024-05-06 10:00:00',
  url: 'https://example.lanzoul.com/abc',
  opinion: '<p>好玩&nbsp;的团</p>',
  moduleVersion: '1.0',
  occurrencePlace: '上海',
  original: '',
  local: null,
  ...over,
})

const buttons = () => Array.from(document.body.querySelectorAll('button'))
const buttonByText = (text: string) => buttons().find((b) => b.textContent?.trim() === text) as HTMLButtonElement | undefined

// 弹窗和面板依赖的任务进度来自 store；测试里用假 WebSocket 推帧，走和生产一样的路径
function connectTasks() {
  useOnlineTasksStore().acquire()
  FakeWebSocket.last.recv({ type: 'snapshot', data: { modules: [] } })
}
const pushModule = (m: OnlineModule) => FakeWebSocket.last.recv({ type: 'module', data: m })

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.setItem('coc_token', 't')
  document.body.innerHTML = ''
  calls = []
  routes = {}
  vi.unstubAllGlobals()
  stubFetch()
  FakeWebSocket.install()
})

afterEach(() => {
  useOnlineTasksStore().release()
  vi.unstubAllGlobals()
  localStorage.clear()
})

describe('OnlineModuleDetailModal', () => {
  const mountModal = () =>
    mount(OnlineModuleDetailModal, { props: { open: true, moduleId: 7 }, attachTo: document.body, global: { plugins: [] } })

  test('命中版权且未放行：编译按钮禁用并写明原因；放行（danger 确认）后可编译', async () => {
    let current = detail({}, [doc({ license_hits: ['禁止投喂AI'], preview: '前文禁止投喂AI后文' })])
    routes['GET /api/admin/online-modules/7'] = () => current
    routes['POST /api/admin/online-modules/7/license-override'] = () => {
      current = detail({ license_override_at: '2026-11-14T09:00:00Z', license_override_by: 1 }, current.documents)
      return current.module
    }
    const w = mountModal()
    await flushPromises()

    const compile = buttonByText('编译')!
    expect(compile.disabled).toBe(true)
    expect(document.body.querySelector('[data-testid="compile-block-reason"]')?.textContent).toBe('所选文档命中版权筛查，需先放行')
    // 命中片段在预览里高亮
    expect(document.body.querySelector('mark')?.textContent).toBe('禁止投喂AI')

    const ui = useUiStore()
    buttonByText('放行')!.click()
    await flushPromises()
    expect(ui.confirmState).toMatchObject({ danger: true, confirmText: '放行' })
    expect(ui.confirmState?.message).toContain('你确认已阅读原文并认为可以编译')
    ui.resolveConfirm(true)
    await flushPromises()

    expect(calls.some((c) => c.method === 'POST' && c.path === '/api/admin/online-modules/7/license-override')).toBe(true)
    expect(buttonByText('编译')!.disabled).toBe(false)
    expect(document.body.querySelector('[data-testid="compile-block-reason"]')).toBeNull()
    expect(w.emitted('changed')).toHaveLength(1)
    w.unmount()
  })

  test('乱码文档：显示「疑似乱码」徽标并禁用编译；OCR 文档显示「OCR 识别」徽标', async () => {
    routes['GET /api/admin/online-modules/7'] = detail({}, [
      doc({ id: 1, path: 'bad.pdf', kind: 'pdf', runes: 9000, garbled: true, candidate: false }),
      doc({ id: 2, path: 'scan.pdf', kind: 'pdf', runes: 5000, ocr: true }),
    ])
    const w = mountModal()
    await flushPromises()

    const text = document.body.textContent ?? ''
    expect(text).toContain('疑似乱码')
    expect(text).toContain('OCR 识别')
    // 默认选中的是候选文档（OCR 那篇），可以编译；切到乱码那篇后禁用并写明原因
    expect(buttonByText('编译')!.disabled).toBe(false)
    const rows = Array.from(document.body.querySelectorAll<HTMLButtonElement>('button[aria-pressed]'))
    rows.find((b) => b.textContent?.includes('bad.pdf'))!.click()
    await flushPromises()
    expect(buttonByText('编译')!.disabled).toBe(true)
    expect(document.body.querySelector('[data-testid="compile-block-reason"]')?.textContent).toBe('所选文档的文字提取结果是乱码')
    w.unmount()
  })

  test('取消放行确认：不发请求，编译仍禁用', async () => {
    routes['GET /api/admin/online-modules/7'] = detail({}, [doc({ license_hits: ['x'] })])
    const w = mountModal()
    await flushPromises()
    const ui = useUiStore()
    buttonByText('放行')!.click()
    await flushPromises()
    ui.resolveConfirm(false)
    await flushPromises()
    expect(calls.some((c) => c.path.endsWith('/license-override'))).toBe(false)
    expect(buttonByText('编译')!.disabled).toBe(true)
    w.unmount()
  })

  test('默认选中字数最多的候选文档；其余原因：未提取 / 正文超过 500KB', async () => {
    routes['GET /api/admin/online-modules/7'] = detail({}, [
      doc({ id: 1, path: 'big.pdf', runes: 9000, candidate: false }),
      doc({ id: 2, path: 'main.docx', runes: 600_000 }),
      doc({ id: 3, path: 'short.txt', runes: 800 }),
    ])
    const w = mountModal()
    await flushPromises()
    const pressed = Array.from(document.body.querySelectorAll('button[aria-pressed="true"]'))
    expect(pressed).toHaveLength(1)
    expect(pressed[0]!.textContent).toContain('main.docx')
    expect(document.body.querySelector('[data-testid="compile-block-reason"]')?.textContent).toBe('正文超过 500KB')
    w.unmount()

    routes['GET /api/admin/online-modules/7'] = detail({ status: 'failed', error: '被拦截' }, [])
    const w2 = mountModal()
    await flushPromises()
    expect(buttonByText('编译')!.disabled).toBe(true)
    expect(document.body.querySelector('[data-testid="compile-block-reason"]')?.textContent).toBe('未提取')
    expect(document.body.textContent).toContain('自动下载失败，请打开分享页手动下载后上传')
    w2.unmount()
  })

  test('字数落在字节数不确定的区间：自动加载全文，按实际字节数放行，加载前禁用', async () => {
    routes['GET /api/admin/online-modules/7'] = detail({}, [doc({ id: 5, runes: 300_000 })])
    // 30 万个 ASCII 字符 = 30 万字节，没超过 512KB
    routes['GET /api/admin/online-modules/7/documents/5'] = { ...doc({ id: 5, runes: 300_000 }), text: 'a'.repeat(300_000) }
    const w = mountModal()
    await flushPromises()
    expect(calls.some((c) => c.path === '/api/admin/online-modules/7/documents/5')).toBe(true)
    expect(buttonByText('编译')!.disabled).toBe(false)
    w.unmount()
  })

  test('编译：SSE 的 done 带 scenario_id 后刷新详情，显示「已编译为模组 #id」', async () => {
    let compiled = false
    routes['GET /api/admin/online-modules/7'] = () => detail(compiled ? { scenario_id: 42 } : {})
    const enc = new TextEncoder()
    const signals: AbortSignal[] = []
    const base = globalThis.fetch as unknown as (path: string, init?: RequestInit) => Promise<Response>
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: string, init?: RequestInit) => {
        if (path.endsWith('/compile')) {
          calls.push({ method: 'POST', path, body: JSON.parse(String(init?.body)) })
          signals.push(init?.signal as AbortSignal)
          compiled = true
          const body = new ReadableStream<Uint8Array>({
            start(c) {
              c.enqueue(enc.encode('event: progress\ndata: {"stage":"compile","status":"start","detail":"编译中"}\n\n'))
              c.enqueue(enc.encode('event: done\ndata: {"index":1,"scenario_id":42,"name":"雨夜档案"}\n\n'))
              c.close()
            },
          })
          return { ok: true, status: 200, body } as unknown as Response
        }
        return base(path, init)
      }),
    )
    const w = mountModal()
    await flushPromises()
    buttonByText('编译')!.click()
    await flushPromises()

    const post = calls.find((c) => c.path === '/api/admin/online-modules/7/compile')!
    expect(post.body).toStrictEqual({ document_id: 11 })
    expect(document.body.textContent).toContain('已编译为模组 #42')
    expect(useUiStore().toasts.map((t) => t.type)).toStrictEqual(['success'])
    expect(w.emitted('changed')).toHaveLength(1)
    // 流已正常结束，没有在途请求需要中断
    expect(signals[0]!.aborted).toBe(false)
    w.unmount()
  })

  test('编译中卸载弹窗：中断在途请求，之后不再弹 toast', async () => {
    routes['GET /api/admin/online-modules/7'] = detail()
    const enc = new TextEncoder()
    const signals: AbortSignal[] = []
    const base = globalThis.fetch as unknown as (path: string, init?: RequestInit) => Promise<Response>
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: string, init?: RequestInit) => {
        if (!path.endsWith('/compile')) return base(path, init)
        const signal = init?.signal as AbortSignal
        signals.push(signal)
        const body = new ReadableStream<Uint8Array>({
          start(c) {
            c.enqueue(enc.encode('event: progress\ndata: {"stage":"compile","status":"start","detail":"编译中"}\n\n'))
            signal.addEventListener('abort', () => c.error(new DOMException('aborted', 'AbortError')))
          },
        })
        return { ok: true, status: 200, body } as unknown as Response
      }),
    )
    const w = mountModal()
    await flushPromises()
    buttonByText('编译')!.click()
    await flushPromises()
    expect(document.body.textContent).toContain('编译中')
    // 编译期间按钮保持禁用，不会重复提交
    expect(buttonByText('编译中…')!.disabled).toBe(true)

    expect(signals[0]!.aborted).toBe(false)
    w.unmount()
    expect(signals[0]!.aborted).toBe(true)
    await flushPromises()
    expect(useUiStore().toasts).toStrictEqual([])
  })

  test('关闭弹窗同样中断在途编译', async () => {
    routes['GET /api/admin/online-modules/7'] = detail()
    const signals: AbortSignal[] = []
    const base = globalThis.fetch as unknown as (path: string, init?: RequestInit) => Promise<Response>
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: string, init?: RequestInit) => {
        if (!path.endsWith('/compile')) return base(path, init)
        const signal = init?.signal as AbortSignal
        signals.push(signal)
        const body = new ReadableStream<Uint8Array>({
          start(c) {
            signal.addEventListener('abort', () => c.error(new DOMException('aborted', 'AbortError')))
          },
        })
        return { ok: true, status: 200, body } as unknown as Response
      }),
    )
    const w = mountModal()
    await flushPromises()
    buttonByText('编译')!.click()
    await flushPromises()
    await w.setProps({ open: false })
    await flushPromises()
    expect(signals[0]!.aborted).toBe(true)
    expect(useUiStore().toasts).toStrictEqual([])
    w.unmount()
  })

  test('上传兜底：multipart 字段名为 file，返回 202 + 模组记录，只提示已开始，不当详情套用', async () => {
    routes['GET /api/admin/online-modules/7'] = detail({ status: 'failed', error: 'x' }, [])
    const accepted = module_({ status: 'downloading', stage: 'extract', task_started_at: '2026-11-14T09:00:00Z' })
    const post = vi.fn(async (_path: string, init?: RequestInit) => {
      calls.push({ method: 'POST', path: _path, body: init?.body })
      return { ok: true, status: 202, json: async () => accepted } as unknown as Response
    })
    const base = globalThis.fetch as unknown as (path: string, init?: RequestInit) => Promise<Response>
    vi.stubGlobal('fetch', (path: string, init?: RequestInit) => (path.endsWith('/archive') ? post(path, init) : base(path, init)))

    const w = mountModal()
    await flushPromises()
    const input = document.body.querySelector('input[type="file"]') as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [new File(['zip'], 'mod.zip')], configurable: true })
    buttonByText('上传')!.click()
    await flushPromises()

    const sent = calls.find((c) => c.path === '/api/admin/online-modules/7/archive')!.body as FormData
    expect(sent).toBeInstanceOf(FormData)
    expect((sent.get('file') as File).name).toBe('mod.zip')
    expect(useUiStore().toasts.map((t) => [t.message, t.type])).toStrictEqual([['已开始提取，完成后会通知你', 'success']])
    expect(w.emitted('changed')).toStrictEqual([[accepted]])
    // 弹窗改为显示进度，而不是把 202 的响应当详情渲染；上传期间不再发详情请求
    expect(calls.filter((c) => c.method === 'GET' && c.path === '/api/admin/online-modules/7')).toHaveLength(1)
    expect(document.body.querySelector('[data-testid="task-progress"]')?.textContent).toContain('提取正文')
    expect(document.body.textContent).not.toContain('自动下载失败，请打开分享页')
    w.unmount()
  })

  test('下载中：显示阶段、备注和已用时，禁用上传；原来的「点刷新」文案不再出现', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date('2026-11-14T09:02:05Z'))
    routes['GET /api/admin/online-modules/7'] = detail(
      { status: 'downloading', stage: 'ocr', stage_note: 'scan.pdf', task_started_at: '2026-11-14T09:00:00Z' },
      [],
    )
    const w = mountModal()
    await flushPromises()

    const progress = document.body.querySelector('[data-testid="task-progress"]')!
    expect(progress.textContent).toContain('OCR 识别')
    expect(progress.textContent).toContain('scan.pdf')
    expect(progress.textContent).toContain('2分05秒')
    expect(document.body.textContent).not.toContain('稍后点')
    expect(buttonByText('上传')!.disabled).toBe(true)

    vi.advanceTimersByTime(1000)
    await flushPromises()
    expect(document.body.querySelector('[data-testid="task-progress"]')!.textContent).toContain('2分06秒')
    w.unmount()
    expect(vi.getTimerCount()).toBe(0)
    vi.useRealTimers()
  })

  test('下载中：收到该模组的 WebSocket 更新后阶段文字随之变化；结束后重新加载详情并通知外层', async () => {
    let current = detail({ status: 'downloading', stage: 'download', task_started_at: '2026-11-14T09:00:00Z' }, [])
    routes['GET /api/admin/online-modules/7'] = () => current
    connectTasks()
    const w = mountModal()
    await flushPromises()
    expect(document.body.querySelector('[data-testid="task-progress"]')!.textContent).toContain('下载压缩包')

    pushModule(module_({ status: 'downloading', stage: 'extract', task_started_at: '2026-11-14T09:00:00Z' }))
    await flushPromises()
    expect(document.body.querySelector('[data-testid="task-progress"]')!.textContent).toContain('提取正文')
    // 别的模组的更新不影响当前弹窗
    pushModule(module_({ id: 99, status: 'failed', error: '别人的' }))
    await flushPromises()
    expect(document.body.textContent).not.toContain('别人的')

    const before = calls.filter((c) => c.path === '/api/admin/online-modules/7').length
    current = detail({ status: 'extracted', archive_name: 'done.zip' }, [doc({ id: 31, path: 'done.docx' })])
    pushModule(current.module)
    await flushPromises()

    expect(calls.filter((c) => c.path === '/api/admin/online-modules/7').length).toBe(before + 1)
    expect(document.body.querySelector('[data-testid="task-progress"]')).toBeNull()
    expect(document.body.textContent).toContain('done.docx')
    expect(w.emitted('changed')).toHaveLength(1)
    expect(buttonByText('上传')!.disabled).toBe(false)
    w.unmount()
  })

  test('结束时保留已选文档（keepSelection）', async () => {
    // 重新上传期间详情里还是旧文档，状态是下载中
    let current = detail({ status: 'downloading', stage: 'extract' }, [
      doc({ id: 1, path: 'a.docx', runes: 9000 }),
      doc({ id: 2, path: 'b.docx', runes: 100, candidate: false }),
    ])
    routes['GET /api/admin/online-modules/7'] = () => current
    connectTasks()
    const w = mountModal()
    await flushPromises()
    const rows = () => Array.from(document.body.querySelectorAll<HTMLButtonElement>('button[aria-pressed]'))
    rows().find((b) => b.textContent?.includes('b.docx'))!.click()
    await flushPromises()

    current = detail({ status: 'extracted' }, current.documents)
    pushModule(current.module)
    await flushPromises()
    expect(rows().find((b) => b.getAttribute('aria-pressed') === 'true')!.textContent).toContain('b.docx')
    w.unmount()
  })

  test('error 非空且状态不是 failed：显示「最近一次任务失败」；failed 状态走原来的手动下载提示，不重复显示', async () => {
    routes['GET /api/admin/online-modules/7'] = detail({ status: 'extracted', error: '压缩包损坏' })
    const w = mountModal()
    await flushPromises()
    expect(document.body.textContent).toContain('最近一次任务失败：压缩包损坏')
    expect(buttonByText('编译')!.disabled).toBe(false)
    w.unmount()

    routes['GET /api/admin/online-modules/7'] = detail({ status: 'failed', error: '被拦截' }, [])
    const w2 = mountModal()
    await flushPromises()
    expect(document.body.textContent).not.toContain('最近一次任务失败')
    expect(document.body.textContent).toContain('被拦截')
    w2.unmount()

    routes['GET /api/admin/online-modules/7'] = detail({ status: 'extracted', error: '' })
    const w3 = mountModal()
    await flushPromises()
    expect(document.body.textContent).not.toContain('最近一次任务失败')
    w3.unmount()
  })

  test('卸载后不再响应 store 的变化', async () => {
    routes['GET /api/admin/online-modules/7'] = detail({ status: 'downloading', stage: 'download' }, [])
    connectTasks()
    const w = mountModal()
    await flushPromises()
    const n = calls.length
    w.unmount()
    pushModule(module_({ status: 'extracted' }))
    await flushPromises()
    expect(calls.length).toBe(n)
  })

  test('上传：扩展名不合法时提示且不发请求', async () => {
    routes['GET /api/admin/online-modules/7'] = detail()
    const w = mountModal()
    await flushPromises()
    const input = document.body.querySelector('input[type="file"]') as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [new File(['x'], 'a.rar')], configurable: true })
    buttonByText('上传')!.click()
    await flushPromises()
    expect(calls.some((c) => c.path.endsWith('/archive'))).toBe(false)
    expect(useUiStore().toasts.map((t) => t.message)).toStrictEqual(['仅支持 .zip / .docx / .pdf 格式'])
    w.unmount()
  })
})

describe('OnlineBrowsePanel', () => {
  test('下载：202 立即返回，提示「已开始下载」并把行标成下载中，不再有「最长约 3 分钟」的提示', async () => {
    routes['GET /api/admin/online-modules/browse'] = { items: [browseItem()], page: 1, total_pages: 3, total_elements: 25 }
    routes['POST /api/admin/online-modules/download'] = module_({ status: 'downloading', stage: 'resolve', task_started_at: '2026-11-14T09:00:00Z' })
    const w = mount(OnlineBrowsePanel, { attachTo: document.body })
    await flushPromises()

    expect(document.body.textContent).toContain('未下载')
    expect(document.body.textContent).toContain('好玩 的团')
    expect(document.body.textContent).toContain('共 25 个 · 第 1 / 3 页')
    expect(document.body.querySelector('[role="alert"]')).toBeNull()

    buttonByText('下载')!.click()
    await flushPromises()

    const sent = calls.find((c) => c.path === '/api/admin/online-modules/download')!.body as Record<string, unknown>
    expect(sent.keyId).toBe('1850')
    expect('local' in sent).toBe(false)
    expect(useUiStore().toasts.map((t) => [t.message, t.type])).toStrictEqual([['已开始下载：雨夜档案，可在「任务进度」页查看', 'success']])
    // 请求已结束，但条目仍是 downloading：按钮保持「下载中…」并禁用，防止重复下载
    expect(buttonByText('下载中…')!.disabled).toBe(true)
    expect(document.body.textContent).toContain('下载中')
    expect(document.body.textContent).not.toContain('最长约 3 分钟')
    expect(document.body.querySelector('[role="alert"]')).toBeNull()
    w.unmount()
  })

  test('下载结果经 WebSocket 到达：applyModule 收到 failed 时显示失败提示和分享页链接；之后重新下载会清掉提示', async () => {
    routes['GET /api/admin/online-modules/browse'] = {
      items: [browseItem({ local: { id: 7, status: 'downloading', scenario_id: null } })],
      page: 1,
      total_pages: 1,
      total_elements: 1,
    }
    const w = mount(OnlineBrowsePanel, { attachTo: document.body })
    await flushPromises()
    expect(buttonByText('下载中…')!.disabled).toBe(true)
    expect(document.body.querySelector('[role="alert"]')).toBeNull()

    const panel = w.vm as unknown as { applyModule: (m: OnlineModule) => void }
    panel.applyModule(module_({ status: 'failed', error: '下载被蓝奏云拦截' }))
    await flushPromises()

    const alert = document.body.querySelector('[role="alert"]')!
    expect(alert.textContent).toContain('自动下载失败，请打开分享页手动下载后上传')
    expect(alert.textContent).toContain('下载被蓝奏云拦截')
    const link = alert.querySelector('a')!
    expect(link.getAttribute('href')).toBe('https://example.lanzoul.com/abc')
    expect(link.getAttribute('rel')).toContain('noopener')
    expect(document.body.textContent).toContain('失败')
    expect(buttonByText('重新下载')).toBeDefined()

    // 新一轮任务开始（或上传后恢复为已提取）：失败提示消失
    panel.applyModule(module_({ status: 'downloading', stage: 'resolve' }))
    await flushPromises()
    expect(document.body.querySelector('[role="alert"]')).toBeNull()
    w.unmount()
  })

  test('409：已有下载在进行，原样提示后端消息，行状态不变', async () => {
    routes['GET /api/admin/online-modules/browse'] = { items: [browseItem()], page: 1, total_pages: 1, total_elements: 1 }
    const base = globalThis.fetch as unknown as (path: string, init?: RequestInit) => Promise<Response>
    vi.stubGlobal('fetch', (path: string, init?: RequestInit) =>
      path.endsWith('/download')
        ? Promise.resolve({ ok: false, status: 409, json: async () => ({ error: '该模组正在下载中' }) } as unknown as Response)
        : base(path, init),
    )
    const w = mount(OnlineBrowsePanel, { attachTo: document.body })
    await flushPromises()
    buttonByText('下载')!.click()
    await flushPromises()
    expect(useUiStore().toasts.map((t) => [t.message, t.type])).toStrictEqual([['该模组正在下载中', 'error']])
    // POST 结束后短暂的防重复状态已解除
    expect(buttonByText('下载')!.disabled).toBe(false)
    w.unmount()
  })

  test('POST 在途期间按钮 loading 且禁用，重复点击只发一次请求；请求结束后防重复状态解除', async () => {
    routes['GET /api/admin/online-modules/browse'] = { items: [browseItem()], page: 1, total_pages: 1, total_elements: 1 }
    let finish: (m: OnlineModule) => void = () => {}
    const pending = new Promise<OnlineModule>((resolve) => (finish = resolve))
    const base = globalThis.fetch as unknown as (path: string, init?: RequestInit) => Promise<Response>
    vi.stubGlobal('fetch', (path: string, init?: RequestInit) => {
      if (path.endsWith('/download')) {
        calls.push({ method: 'POST', path, body: undefined })
        return pending.then((m) => ({ ok: true, status: 202, json: async () => m }) as unknown as Response)
      }
      return base(path, init)
    })
    const w = mount(OnlineBrowsePanel, { attachTo: document.body })
    await flushPromises()

    const btn = buttonByText('下载')!
    btn.click()
    await flushPromises()
    const busy = buttonByText('下载中…')!
    expect(busy.disabled).toBe(true)
    busy.click()
    await flushPromises()
    expect(calls.filter((c) => c.path.endsWith('/download'))).toHaveLength(1)

    finish(module_({ status: 'downloading', stage: 'resolve' }))
    await flushPromises()
    expect(document.body.textContent).toContain('下载中')
    expect(document.body.querySelector('[role="alert"]')).toBeNull()

    // 之后任务结束（WebSocket 推送）：按钮恢复为不可见（已提取），状态标签更新
    ;(w.vm as unknown as { applyModule: (m: OnlineModule) => void }).applyModule(module_({ status: 'extracted' }))
    await flushPromises()
    expect(document.body.textContent).toContain('已提取')
    expect(buttonByText('下载')).toBeUndefined()
    w.unmount()
  })

  test('搜索带 title 参数并回到第 1 页，翻页沿用已提交的搜索词', async () => {
    routes['GET /api/admin/online-modules/browse'] = { items: [browseItem()], page: 1, total_pages: 3, total_elements: 25 }
    const w = mount(OnlineBrowsePanel, { attachTo: document.body })
    await flushPromises()
    expect(calls[0]!.path).toBe('/api/admin/online-modules/browse?page=1')

    const input = document.body.querySelector('input[type="search"]') as HTMLInputElement
    input.value = ' 雨夜 '
    input.dispatchEvent(new Event('input'))
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(calls.at(-1)!.path).toBe('/api/admin/online-modules/browse?page=1&title=' + encodeURIComponent('雨夜'))

    buttonByText('下一页')!.click()
    await flushPromises()
    expect(calls.at(-1)!.path).toBe('/api/admin/online-modules/browse?page=2&title=' + encodeURIComponent('雨夜'))
    w.unmount()
  })

  test('已编译的条目显示「已编译」，已提取的条目不再显示下载按钮', async () => {
    routes['GET /api/admin/online-modules/browse'] = {
      items: [
        browseItem({ keyId: '1', title: 'A', local: { id: 1, status: 'extracted', scenario_id: 9 } }),
        browseItem({ keyId: '2', title: 'B', local: { id: 2, status: 'extracted', scenario_id: null } }),
      ],
      page: 1,
      total_pages: 1,
      total_elements: 2,
    }
    const w = mount(OnlineBrowsePanel, { attachTo: document.body })
    await flushPromises()
    expect(document.body.textContent).toContain('已编译')
    expect(document.body.textContent).toContain('已提取')
    expect(buttonByText('下载')).toBeUndefined()
    expect(buttons().filter((b) => b.textContent?.trim() === '详情')).toHaveLength(2)
    w.unmount()
  })
})
