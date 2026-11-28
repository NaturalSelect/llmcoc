// @vitest-environment jsdom
// NOTE: 挂载真实的分类器面板，用假 fetch 和真实的 roomState 归约：打开时请求历史、筛选、展开详情、历史与实时事件合并去重、失败重试、非管理员不请求
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { reactive } from 'vue'
import { createAppRouter } from '@/router'
import * as RS from '@/game/roomState'
import type { Room } from '@/game/roomState'
import { useAuthStore } from '@/stores/auth'
import ClassifierPanel from './ClassifierPanel.vue'

const HISTORY_PATH = '/api/admin/sessions/7/classifier-logs?limit=200'

function json(body: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => body } as Response
}

// NOTE: 时间串不带时区，按本地时间解析，所以「10:20:30」与运行机器的时区无关
function L(id: number, extra: Record<string, unknown> = {}) {
  return {
    id,
    kind: 'input_guard',
    created_at: '2026-11-28T10:20:30',
    user_id: 3,
    subject: `调查员${id}`,
    floor: 0,
    decision: 'passed',
    status: 'ok',
    error: '',
    choice: 'in_character',
    score: 0.1,
    threshold: 0.8,
    probabilities: { in_character: 0.9, authority_claim: 0.1 },
    request: { player_input: '我推门进去' },
    role: '',
    elapsed_ms: 412,
    request_id: '',
    ...extra,
  }
}

const NSFW = { kind: 'nsfw_route', decision: 'nsfw', floor: 5, role: 'director_nsfw', score: 0.72, threshold: 0.5, choice: 'sexual_suggestive' }

let fetchCalls: string[] = []
let handler: (path: string) => Response = () => json({ logs: [] })

function newRoom(): Room {
  return reactive(RS.newRoom({ sessionId: 7, userId: 2, isHost: true })) as Room
}

// NOTE: 服务端用不带 seq 的临时帧推 classifier_log
function realtime(room: Room, log: Record<string, unknown>) {
  RS.applyFrame(room, { seq: 0, type: 'classifier_log', data: { log } })
}

async function mountPanel({ admin = true, open = true, room = newRoom() }: { admin?: boolean; open?: boolean; room?: Room } = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  localStorage.setItem('coc_token', 'tok')
  const auth = useAuthStore()
  auth.user = { id: 2, username: 'alice', role: admin ? 'admin' : 'user' }
  auth.meLoaded = true
  const router = createAppRouter(createMemoryHistory())
  const wrapper = mount(ClassifierPanel, {
    props: { open, sessionId: 7, room },
    global: { plugins: [pinia, router] },
    attachTo: document.body,
  })
  await flushPromises()
  return { wrapper, room }
}

const rows = () => [...document.body.querySelectorAll<HTMLElement>('[data-testid="classifier-row"]')]
const subjects = () => rows().map((r) => r.querySelector('[data-testid="row-subject"]')!.textContent)
const historyCalls = () => fetchCalls.filter((p) => p.startsWith('/api/admin/sessions/'))
const buttonByText = (text: string) => [...document.body.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent?.trim() === text)
async function click(el: Element | undefined | null) {
  expect(el).toBeTruthy()
  ;(el as HTMLElement).click()
  await flushPromises()
}
const rowButton = (idx: number) => rows()[idx].querySelector('button')

beforeEach(() => {
  document.body.innerHTML = ''
  localStorage.clear()
  fetchCalls = []
  handler = () => json({ logs: [] })
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string) => {
      fetchCalls.push(path)
      return handler(path)
    }),
  )
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ClassifierPanel', () => {
  test('打开时请求一次历史；标题、静态提示和跳到后台设置的链接', async () => {
    handler = () => json({ logs: [L(1)] })
    const { wrapper } = await mountPanel()
    expect(historyCalls()).toStrictEqual([HISTORY_PATH])
    const dialog = document.body.querySelector('[role="dialog"]')!
    expect(dialog.getAttribute('aria-label')).toBe('分类器记录')
    expect(dialog.querySelector('h2')!.textContent).toBe('分类器记录')
    expect(dialog.textContent).toContain('阈值在')
    expect(dialog.textContent).toContain('里调整')
    const link = [...dialog.querySelectorAll('a')].find((a) => a.textContent === '管理 → 系统设置')!
    expect(link.getAttribute('href')).toBe('/admin/settings')
    wrapper.unmount()
  })

  test('没打开时不请求；打开后才请求，不轮询', async () => {
    vi.useFakeTimers()
    try {
      const { wrapper } = await mountPanel({ open: false })
      expect(historyCalls()).toStrictEqual([])
      expect(document.body.querySelector('[role="dialog"]')).toBeNull()

      await wrapper.setProps({ open: true })
      await flushPromises()
      expect(historyCalls()).toHaveLength(1)

      await vi.advanceTimersByTimeAsync(10 * 60 * 1000)
      expect(historyCalls()).toHaveLength(1)
      wrapper.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  test('列表新的在上；一行显示时间、kind、subject、决定、分数 / 阈值、耗时', async () => {
    handler = () =>
      json({
        logs: [
          L(1, { decision: 'blocked', score: 0.85, threshold: 0.8, subject: '梅布尔', elapsed_ms: 1500 }),
          L(2, { ...NSFW, subject: '甲、乙', score: 0.5, threshold: 0.5, elapsed_ms: 0 }),
        ],
      })
    const { wrapper } = await mountPanel()
    expect(subjects()).toStrictEqual(['甲、乙', '梅布尔'])

    const [top, bottom] = rows()
    expect(top.querySelector('[data-testid="row-time"]')!.textContent).toBe('10:20:30')
    expect(top.querySelector('[data-testid="row-kind"]')!.textContent).toBe('NSFW 路由')
    expect(top.querySelector('[data-testid="row-decision"]')!.textContent).toBe('涉及 NSFW')
    expect(top.querySelector('[data-testid="row-score"]')!.textContent).toBe('0.50 / 0.50')
    expect(top.querySelector('[data-testid="row-elapsed"]')!.textContent).toBe('0ms')

    expect(bottom.querySelector('[data-testid="row-kind"]')!.textContent).toBe('输入分类')
    expect(bottom.querySelector('[data-testid="row-decision"]')!.textContent).toBe('已拒收')
    expect(bottom.querySelector('[data-testid="row-score"]')!.textContent).toBe('0.85 / 0.80')
    expect(bottom.querySelector('[data-testid="row-elapsed"]')!.textContent).toBe('1.5s')
    expect(document.body.querySelector('[data-testid="classifier-count"]')!.textContent).toBe('共 2 条')
    wrapper.unmount()
  })

  test('决定标签的色调：拒收红、涉及 NSFW 琥珀、失败和出错警示、放行和不涉及绿；出错的分数不显示', async () => {
    handler = () =>
      json({
        logs: [
          L(1, { decision: 'blocked', score: 0.9 }),
          L(2, NSFW),
          L(3, { kind: 'nsfw_route', decision: 'failed', status: 'error', error: 'HTTP 401', score: 0, threshold: 0.5, probabilities: {}, choice: '' }),
          L(4, { decision: 'passed', status: 'error', error: '超时', score: 0, probabilities: {}, choice: '' }),
          L(5, { decision: 'passed' }),
          L(6, { kind: 'nsfw_route', decision: 'clean' }),
        ],
      })
    const { wrapper } = await mountPanel()
    const decision = (idx: number) => rows()[idx].querySelector('[data-testid="row-decision"]')!
    // NOTE: 新的在上：行顺序是 id 6、5、4、3、2、1
    expect(decision(0).className).toContain('text-success')
    expect(decision(1).className).toContain('text-success')
    expect(decision(2).className).toContain('text-warning')
    expect(decision(2).textContent).toBe('放行（出错）')
    expect(decision(3).className).toContain('text-warning')
    expect(decision(3).textContent).toBe('失败')
    expect(decision(4).className).toContain('text-accent')
    expect(decision(5).className).toContain('text-danger')
    expect(rows()[2].querySelector('[data-testid="row-score"]')!.textContent).toBe('—')
    expect(rows()[3].querySelector('[data-testid="row-score"]')!.textContent).toBe('—')
    wrapper.unmount()
  })

  test('按 kind 筛选：全部 / 输入分类 / NSFW 路由', async () => {
    handler = () => json({ logs: [L(1), L(2, NSFW), L(3), L(4, NSFW)] })
    const { wrapper } = await mountPanel()
    expect(subjects()).toStrictEqual(['调查员4', '调查员3', '调查员2', '调查员1'])

    await click(buttonByText('输入分类'))
    expect(subjects()).toStrictEqual(['调查员3', '调查员1'])
    await click(buttonByText('NSFW 路由'))
    expect(subjects()).toStrictEqual(['调查员4', '调查员2'])
    await click(buttonByText('全部'))
    expect(subjects()).toHaveLength(4)
    wrapper.unmount()
  })

  test('空状态：没有记录给一句说明；筛选后为空给另一句', async () => {
    const { wrapper } = await mountPanel()
    expect(rows()).toHaveLength(0)
    expect(document.body.textContent).toContain('暂无分类记录')
    expect(document.body.textContent).toContain('共 0 条')
    wrapper.unmount()

    document.body.innerHTML = ''
    handler = () => json({ logs: [L(1)] })
    const second = await mountPanel()
    await click(buttonByText('NSFW 路由'))
    expect(rows()).toHaveLength(0)
    expect(document.body.textContent).toContain('这个分类下暂无记录')
    expect(document.body.textContent).not.toContain('暂无分类记录')
    second.wrapper.unmount()
  })

  test('点击一行展开详情，再点收起；概率降序、最高项标出，分数与阈值对比', async () => {
    handler = () =>
      json({
        logs: [
          L(1, {
            decision: 'blocked',
            choice: 'authority_claim',
            score: 0.85,
            threshold: 0.8,
            probabilities: { in_character: 0.1, table_talk: 0.05, authority_claim: 0.55, spoiler_request: 0.3, brand_new: 0 },
            user_id: 3,
            request_id: 'req_abc',
            elapsed_ms: 412,
          }),
        ],
      })
    const { wrapper } = await mountPanel()
    expect(document.body.querySelector('[data-testid="classifier-detail"]')).toBeNull()
    expect(rowButton(0)!.getAttribute('aria-expanded')).toBe('false')

    await click(rowButton(0))
    expect(rowButton(0)!.getAttribute('aria-expanded')).toBe('true')
    const detail = document.body.querySelector('[data-testid="classifier-detail"]')!
    const probs = [...detail.querySelectorAll<HTMLElement>('[data-testid="prob-row"]')]
    expect(probs.map((p) => p.querySelector('span')!.textContent)).toStrictEqual(['自称权威', '索要谜底', '角色内行动', '桌边闲聊', 'brand_new'])
    expect(probs.map((p) => p.getAttribute('data-top'))).toStrictEqual(['true', 'false', 'false', 'false', 'false'])
    expect(probs[0].textContent).toContain('55.0%')
    expect(probs[0].textContent).toContain('最高')
    expect(probs[1].textContent).not.toContain('最高')
    expect(probs[0].querySelector<HTMLElement>('[role="progressbar"] > div')!.style.width).toBe('55%')
    expect(probs[0].querySelector('[role="progressbar"] > div')!.className).toContain('bg-accent')
    expect(probs[1].querySelector('[role="progressbar"] > div')!.className).not.toContain('bg-accent')

    expect(detail.querySelector('[data-testid="detail-verdict"]')!.textContent!.replace(/\s+/g, ' ').trim()).toBe('分数 0.85 · 阈值 0.80 · 命中（分数 ≥ 阈值）')
    expect(detail.querySelector<HTMLElement>('[data-testid="threshold-mark"]')!.style.left).toBe('80%')
    expect(detail.querySelector('[data-testid="detail-error"]')).toBeNull()
    expect(detail.querySelector('[data-testid="detail-role"]')).toBeNull()
    expect(detail.querySelector('[data-testid="detail-floor"]')).toBeNull()
    expect(detail.querySelector('[data-testid="detail-request-id"]')!.textContent).toBe('req_abc')
    expect(detail.textContent).toContain('412ms')
    expect(detail.textContent).toContain('用户 ID')
    expect(detail.textContent).toContain('2026-11-28 10:20:30')

    await click(rowButton(0))
    expect(document.body.querySelector('[data-testid="classifier-detail"]')).toBeNull()
    wrapper.unmount()
  })

  test('详情：未命中、nsfw_route 的执行角色和周期；floor 为 0、request_id 为空时不显示', async () => {
    handler = () =>
      json({
        logs: [
          L(1, { kind: 'nsfw_route', decision: 'clean', role: 'director', floor: 6, score: 0.2, threshold: 0.5, choice: 'non_sexual', probabilities: { non_sexual: 0.8, sexual_suggestive: 0.2 } }),
          L(2, { kind: 'nsfw_route', decision: 'clean', role: 'director', floor: 0 }),
        ],
      })
    const { wrapper } = await mountPanel()
    await click(rowButton(1))
    let detail = document.body.querySelector('[data-testid="classifier-detail"]')!
    expect(detail.querySelector('[data-testid="detail-role"]')!.textContent).toBe('本周期执行：Director')
    expect(detail.querySelector('[data-testid="detail-floor"]')!.textContent).toBe('6')
    expect(detail.querySelector('[data-testid="detail-request-id"]')).toBeNull()
    expect(detail.querySelector('[data-testid="detail-verdict"]')!.textContent).toContain('未命中（分数 < 阈值）')
    expect(detail.querySelector('[data-testid="detail-verdict"]')!.textContent).toContain('分数 0.20')

    await click(rowButton(0))
    detail = document.body.querySelectorAll('[data-testid="classifier-detail"]')[0]
    expect(detail.querySelector('[data-testid="detail-floor"]')).toBeNull()
    wrapper.unmount()
  })

  test('详情：NSFW Director 的文案；出错记录显示错误原因，没有概率数据和分数条', async () => {
    handler = () =>
      json({
        logs: [
          L(1, { ...NSFW }),
          L(2, { kind: 'nsfw_route', decision: 'failed', status: 'error', error: 'HTTP 400（按安全过滤命中处理）', role: 'director', score: 0, threshold: 0.5, choice: '', probabilities: {}, floor: 3 }),
        ],
      })
    const { wrapper } = await mountPanel()
    await click(rowButton(1))
    expect(document.body.querySelector('[data-testid="detail-role"]')!.textContent).toBe('本周期执行：NSFW Director')
    await click(rowButton(0))

    const detail = document.body.querySelectorAll('[data-testid="classifier-detail"]')[0]
    expect(detail.querySelector('[data-testid="detail-error"]')!.textContent!.trim()).toBe('错误原因：HTTP 400（按安全过滤命中处理）')
    expect(detail.querySelectorAll('[data-testid="prob-row"]')).toHaveLength(0)
    expect(detail.textContent).toContain('没有概率数据')
    expect(detail.querySelector('[data-testid="threshold-mark"]')).toBeNull()
    expect(detail.querySelector('[data-testid="detail-verdict"]')!.textContent).toContain('没有得到有效分数')
    expect(detail.querySelector('[data-testid="detail-role"]')!.textContent).toBe('本周期执行：Director')
    wrapper.unmount()
  })

  test('详情：request 的每个键值用等宽字体原样保留换行；内容是不可信文本，不会被当成 HTML', async () => {
    const evil = '<img src=x onerror=alert(1)><b>加粗</b>\n第二行\n\n  缩进的第四行'
    handler = () =>
      json({
        logs: [L(1, { request: { player_input: evil, player_character: '梅布尔', recent_table_log: '', kp_question: '<script>alert(1)</script>' } })],
      })
    const { wrapper } = await mountPanel()
    await click(rowButton(0))
    const entries = [...document.body.querySelectorAll('[data-testid="request-entry"]')]
    expect(entries.map((e) => e.querySelector('p')!.textContent)).toStrictEqual(['player_input', 'player_character', 'recent_table_log', 'kp_question'])
    const values = entries.map((e) => e.querySelector('pre')!)
    expect(values[0].textContent).toBe(evil)
    expect(values[1].textContent).toBe('梅布尔')
    expect(values[2].textContent).toBe('（空）')
    expect(values[3].textContent).toBe('<script>alert(1)</script>')
    expect(values[0].className).toContain('font-mono')
    expect(values[0].className).toContain('whitespace-pre-wrap')
    expect(document.body.querySelector('[role="dialog"] img')).toBeNull()
    expect(document.body.querySelector('[role="dialog"] script')).toBeNull()
    expect(document.body.querySelector('[role="dialog"] b')).toBeNull()
    wrapper.unmount()
  })

  test('历史与实时事件合并去重：打开前已收到的实时记录不重复，新事件出现在最上面，重复的 id 不再加', async () => {
    const room = newRoom()
    realtime(room, L(3))
    handler = () => json({ logs: [L(1), L(2), L(3)] })
    const { wrapper } = await mountPanel({ room })
    expect(subjects()).toStrictEqual(['调查员3', '调查员2', '调查员1'])
    expect(room.classifierLogs.map((l) => l.id)).toStrictEqual([1, 2, 3])

    realtime(room, L(4))
    await flushPromises()
    expect(subjects()).toStrictEqual(['调查员4', '调查员3', '调查员2', '调查员1'])

    realtime(room, L(4, { subject: '重复的' }))
    realtime(room, L(2))
    await flushPromises()
    expect(subjects()).toStrictEqual(['调查员4', '调查员3', '调查员2', '调查员1'])
    expect(historyCalls()).toHaveLength(1)
    wrapper.unmount()
  })

  test('展开的详情不因新记录到达而收起', async () => {
    handler = () => json({ logs: [L(1)] })
    const { wrapper, room } = await mountPanel()
    await click(rowButton(0))
    realtime(room, L(2))
    await flushPromises()
    expect(subjects()).toStrictEqual(['调查员2', '调查员1'])
    expect(rowButton(1)!.getAttribute('aria-expanded')).toBe('true')
    expect(rowButton(0)!.getAttribute('aria-expanded')).toBe('false')
    wrapper.unmount()
  })

  test('刷新：重新请求历史并按 id 合并，新增的并进来，已有的不重复', async () => {
    let calls = 0
    handler = () => {
      calls++
      return json({ logs: calls === 1 ? [L(1), L(2)] : [L(1), L(2), L(3), L(4)] })
    }
    const { wrapper } = await mountPanel()
    expect(subjects()).toStrictEqual(['调查员2', '调查员1'])
    await click(buttonByText('刷新'))
    expect(historyCalls()).toHaveLength(2)
    expect(subjects()).toStrictEqual(['调查员4', '调查员3', '调查员2', '调查员1'])
    expect(document.body.querySelector('[data-testid="classifier-count"]')!.textContent).toBe('共 4 条')
    wrapper.unmount()
  })

  test('请求失败：给出提示和重试，不影响已收到的实时记录；重试成功后提示消失', async () => {
    let fail = true
    handler = () => (fail ? json({ error: '服务暂时不可用' }, 500) : json({ logs: [L(1)] }))
    const room = newRoom()
    realtime(room, L(9))
    const { wrapper } = await mountPanel({ room })
    const alert = () => document.body.querySelector('[data-testid="classifier-error"]')
    expect(alert()!.textContent).toContain('加载历史记录失败：服务暂时不可用')
    expect(subjects()).toStrictEqual(['调查员9'])

    fail = false
    await click(buttonByText('重试'))
    expect(alert()).toBeNull()
    expect(subjects()).toStrictEqual(['调查员9', '调查员1'])
    expect(historyCalls()).toHaveLength(2)
    wrapper.unmount()
  })

  test('请求失败后点「刷新」也能恢复', async () => {
    let fail = true
    handler = () => (fail ? json({ error: '出错了' }, 500) : json({ logs: [L(1)] }))
    const { wrapper } = await mountPanel()
    expect(document.body.querySelector('[data-testid="classifier-error"]')).not.toBeNull()
    fail = false
    await click(buttonByText('刷新'))
    expect(document.body.querySelector('[data-testid="classifier-error"]')).toBeNull()
    expect(subjects()).toStrictEqual(['调查员1'])
    wrapper.unmount()
  })

  test('响应迟到时房间已经换了：旧房间的响应被丢弃，不并进任何房间；只认最近一次请求', async () => {
    const pending: Array<(r: Response) => void> = []
    vi.stubGlobal(
      'fetch',
      vi.fn((path: string) => {
        fetchCalls.push(path)
        return new Promise<Response>((resolve) => pending.push(resolve))
      }),
    )
    const first = newRoom()
    const { wrapper } = await mountPanel({ room: first })
    const second = newRoom()
    await wrapper.setProps({ room: second })
    await flushPromises()
    expect(pending).toHaveLength(2)

    pending[0](json({ logs: [L(1)] }))
    await flushPromises()
    expect(first.classifierLogs).toHaveLength(0)
    expect(second.classifierLogs).toHaveLength(0)

    pending[1](json({ logs: [L(2)] }))
    await flushPromises()
    expect(second.classifierLogs.map((l) => l.id)).toStrictEqual([2])
    expect(first.classifierLogs).toHaveLength(0)
    wrapper.unmount()
  })

  test('非管理员：即使面板被打开也不发起请求', async () => {
    const { wrapper } = await mountPanel({ admin: false })
    expect(historyCalls()).toStrictEqual([])
    expect(fetchCalls).toStrictEqual([])
    await click(buttonByText('刷新'))
    expect(fetchCalls).toStrictEqual([])
    wrapper.unmount()
  })

  test('点右上角关闭按钮触发 close 事件', async () => {
    const { wrapper } = await mountPanel()
    await click(document.body.querySelector('button[aria-label="关闭"]'))
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
})
