// @vitest-environment jsdom
// NOTE: 挂载真实的 GameView，用假的 fetch 和 WebSocket 走一遍：进房、hello、快照、点名问题卡、提交、房主按钮、离开关闭连接
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { createAppRouter } from '@/router'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'
import GameView from './GameView.vue'

class FakeWS {
  static all: FakeWS[] = []
  onmessage: ((ev: MessageEvent) => void) | null = null
  onclose: (() => void) | null = null
  readyState = 1
  closed = false
  constructor(readonly url: string) {
    FakeWS.all.push(this)
  }
  close() {
    this.closed = true
    this.readyState = 3
  }
  recv(seq: number, type: string, data: Record<string, unknown> = {}) {
    this.onmessage?.({ data: JSON.stringify({ seq, type, data }) } as MessageEvent)
  }
}

const SPOT_ME = { user_ids: [2], names: ['爱丽丝'], question: '你要怎么做？', kp_floor: false, asked_me: true, stop: '' }
// NOTE: KP 持有发言权、没有点名：流里不会出现问题卡，分类器相关用例用它避免问题卡干扰位置断言
const SPOT_KP = { user_ids: [], names: [], question: '', kp_floor: true, asked_me: false, stop: '' }

const kpMessage = {
  id: 1,
  role: 'assistant',
  kind: 'kp',
  status: 'done',
  content: '雨夜里，门廊的灯忽明忽暗。',
  floor: 1,
  created_at: '2026-11-28T10:00:00',
}

const askMessage = {
  id: 2,
  role: 'assistant',
  kind: 'ask',
  status: 'done',
  content: '你要怎么做？',
  floor: 1,
  created_at: '2026-11-28T10:00:20',
  meta: { options: ['推门进去', '绕到后院'], ask: { user_ids: [2], names: ['爱丽丝'] } },
}

const eventMessage = {
  id: 3,
  role: 'assistant',
  kind: 'event',
  status: 'done',
  content: '',
  floor: 1,
  username: 'KP',
  created_at: '2026-11-28T10:00:30',
  meta: {
    events: [
      { type: 'dice', character: '爱丽丝', skill: '侦查', roll: 45, level: '困难' },
      { type: 'dice', hidden: true },
      { type: 'stat', character: '爱丽丝', field: 'hp', from: 10, to: 9 },
    ],
  },
}

function json(body: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => body } as Response
}

// NOTE: 管理员进房、重连后请求的分类器历史接口（limit 写在路径里）；非管理员不会请求
const CLASSIFIER_PATH = '/api/admin/sessions/7/classifier-logs?limit=500'

function classifierLog(id: number, extra: Record<string, unknown> = {}) {
  return {
    id,
    kind: 'input_guard',
    created_at: '2026-11-28T10:20:30',
    user_id: 2,
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

function routeLog(id: number, extra: Record<string, unknown> = {}) {
  return classifierLog(id, {
    kind: 'nsfw_route',
    user_id: 0,
    subject: '爱丽丝',
    decision: 'nsfw',
    choice: 'nsfw',
    score: 0.72,
    threshold: 0.5,
    role: 'director_nsfw',
    probabilities: { sexual_explicit: 0.5, sexual_suggestive: 0.22, clean: 0.28 },
    request: { player_actions: '我推门进去' },
    ...extra,
  })
}

// NOTE: 玩家消息：id 4 起、时间晚于基础消息；floor 对应分类器记录里的 floor
function playerMessage(id: number, floor: number, extra: Record<string, unknown> = {}) {
  return {
    id,
    role: 'user',
    kind: 'player',
    status: 'done',
    content: `行动${id}`,
    floor,
    user_id: 2,
    username: 'alice',
    created_at: `2026-11-28T10:01:${String(id).padStart(2, '0')}`,
    ...extra,
  }
}

interface Setup {
  hostId?: number
  spotlight?: Record<string, unknown>
  extraMessages?: Array<Record<string, unknown>>
  players?: Array<Record<string, unknown>>
  status?: string
  asAdmin?: boolean
  maxPlayers?: number
  handbook?: Record<string, unknown>
  classifierLogs?: Array<Record<string, unknown>>
  // NOTE: 自定义分类器历史接口的响应（可以挂起、失败）；不传就按 classifierLogs 返回，用例中途可以改 setup 返回的 classifier.logs
  classifierRespond?: () => Promise<Response>
}

async function setup({ hostId = 1, spotlight = SPOT_ME, extraMessages = [], players, status = 'playing', asAdmin = false, maxPlayers, handbook, classifierLogs, classifierRespond }: Setup = {}) {
  const fetchCalls: Array<{ method: string; path: string }> = []
  const classifier = { logs: classifierLogs ?? [] }
  const session = {
    id: 7,
    name: '雾港旧宅',
    status,
    created_by: hostId,
    max_players: maxPlayers,
    scenario: { name: '旧宅疑云' },
    players: players ?? [{ user_id: 2, user: { username: 'alice' }, character_card: { id: 5, name: '爱丽丝', occupation: '记者', stats: { hp: 9, max_hp: 10, san: 40, max_san: 50, mp: 8, max_mp: 8 } } }],
  }
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method || 'GET'
      fetchCalls.push({ method, path })
      if (path === '/api/sessions/7') return json(structuredClone(session))
      // NOTE: 8 号房只给「切换房间」的用例用：能进房、有一条消息
      if (path === '/api/sessions/8') return json({ ...structuredClone(session), id: 8, name: '另一个房间' })
      if (path === '/api/sessions/8/messages') return json([kpMessage])
      if (path === '/api/sessions/7/messages') return json([kpMessage, eventMessage, askMessage, ...extraMessages])
      if (path === '/api/sessions/7/chat-status' || path === '/api/sessions/8/chat-status') {
        return json({ kp_running: false, submitted: false, floor: null, spotlight, waiting: { pending: 1, total: 1, submitted_names: [], pending_names: ['爱丽丝'] } })
      }
      if (path === '/api/sessions/7/chat') return json({}, 202)
      if (path === '/api/sessions/7/handbook' && handbook) return json(handbook)
      if (path === '/api/sessions/7/companion-cards') {
        return json({ enabled: true, cards: [{ id: 31, name: '陪玩医生', occupation: '医生', age: 35, gender: '女', portrait_hash: '', hp: 10, max_hp: 10, san: 60, max_san: 60 }] })
      }
      if (path === '/api/admin/messages/4/direction') return json({ id: 4, call_id: 'call_w1', auto: false, direction: '描述门廊，强调霉味' })
      if (path === '/api/admin/messages/5/direction') return json({ id: 5, call_id: '', auto: true, direction: '' })
      if (path === CLASSIFIER_PATH) return classifierRespond ? classifierRespond() : json({ logs: classifier.logs })
      if (path === '/api/characters/5') return json({ id: 5, name: '爱丽丝', occupation: '记者', inventory: [{ name: '最新的怀表', count: 1, type: 'misc' }], stats: { hp: 3, max_hp: 10, san: 20, max_san: 50, mp: 8, max_mp: 8 } })
      if (path === '/api/characters/5/portrait') return json({ portrait_hash: 'FF00AA' })
      return json({})
    }),
  )
  vi.stubGlobal('WebSocket', FakeWS)

  const pinia = createPinia()
  setActivePinia(pinia)
  localStorage.setItem('coc_token', 'tok')
  const auth = useAuthStore()
  auth.user = { id: 2, username: 'alice', role: asAdmin ? 'admin' : 'user' }
  auth.meLoaded = true
  auth.shopCosts = { end_session_cost: 200 }

  const router = createAppRouter(createMemoryHistory())
  await router.push('/sessions/7')
  await router.isReady()

  const wrapper = mount(GameView, { global: { plugins: [pinia, router] }, attachTo: document.body })
  await flushPromises()
  const ws = FakeWS.all[FakeWS.all.length - 1]
  ws.recv(0, 'hello', { epoch: 'E1', seq: 3 })
  await flushPromises()
  return { wrapper, ws, router, fetchCalls, session, classifier }
}

beforeEach(() => {
  FakeWS.all = []
  document.body.innerHTML = ''
  localStorage.clear()
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('GameView', () => {
  test('进房后渲染头部、状态条、历史消息和问题卡；连接带 token', async () => {
    const { wrapper, ws } = await setup()
    expect(ws.url).toContain('/api/sessions/7/ws?token=tok')

    const text = wrapper.text()
    expect(text).toContain('雾港旧宅')
    expect(text).toContain('旧宅疑云')
    expect(text).toContain('游戏中')
    expect(text).toContain('爱丽丝')
    expect(text).toContain('雨夜里，门廊的灯忽明忽暗。')
    expect(text).toContain('KP 向你提问')
    expect(text).toContain('你要怎么做？')
    expect(text).toContain('推门进去')
    expect(wrapper.find('[role="status"]').text()).toContain('KP 在等你回应')
    wrapper.unmount()
  })

  test('工具事件渲染成紧凑列表：公开骰、暗骰、属性变化，没有 KP 表头', async () => {
    const { wrapper } = await setup()
    const list = wrapper.find('ul[aria-label="KP 结算"]')
    expect(list.exists()).toBe(true)
    const rows = list.findAll('li').map((li) => li.findAll('span, b').map((n) => n.text()).join(' '))
    expect(rows).toStrictEqual(['爱丽丝 侦查 45 难度 困难', 'KP 暗骰了一次', '爱丽丝 HP 10 → 9'])
    expect(list.element.closest('article')?.querySelector('.font-medium')).toBeNull()
    wrapper.unmount()
  })

  test('暗钟只在管理员的时钟条上出现，按钮可隐藏并记住选择；普通玩家没有按钮', async () => {
    const handbook = {
      clocks: [
        { name: '血月仪式', current: 2, max: 4, visible: true },
        { name: '教团暗杀倒计时', current: 3, max: 4, visible: false },
      ],
    }
    const admin = await setup({ asAdmin: true, handbook })
    const bar = () => admin.wrapper.find('[aria-label="危机时钟"]')
    expect(bar().text()).toContain('教团暗杀倒计时')
    expect(bar().text()).toContain('隐藏暗钟')

    await admin.wrapper.find('[data-testid="toggle-dark-clocks"]').trigger('click')
    expect(bar().text()).toContain('血月仪式')
    expect(bar().text()).not.toContain('教团暗杀倒计时')
    expect(bar().text()).toContain('显示暗钟 (1)')
    expect(localStorage.getItem('coc_hide_dark_clocks')).toBe('1')

    await admin.wrapper.find('[data-testid="toggle-dark-clocks"]').trigger('click')
    expect(bar().text()).toContain('教团暗杀倒计时')
    expect(localStorage.getItem('coc_hide_dark_clocks')).toBe('0')
    admin.wrapper.unmount()

    document.body.innerHTML = ''
    FakeWS.all = []
    const player = await setup({ handbook: { clocks: [handbook.clocks[0]] } })
    expect(player.wrapper.find('[aria-label="危机时钟"]').text()).toContain('血月仪式')
    expect(player.wrapper.find('[data-testid="toggle-dark-clocks"]').exists()).toBe(false)
    player.wrapper.unmount()
  })

  test('暗钟全部被隐藏时时钟条仍保留按钮，可以再显示出来', async () => {
    localStorage.setItem('coc_hide_dark_clocks', '1')
    const { wrapper } = await setup({
      asAdmin: true,
      handbook: { clocks: [{ name: '教团暗杀倒计时', current: 3, max: 4, visible: false }] },
    })
    const bar = wrapper.find('[aria-label="危机时钟"]')
    expect(bar.text()).not.toContain('教团暗杀倒计时')
    expect(bar.text()).toContain('显示暗钟 (1)')

    await wrapper.find('[data-testid="toggle-dark-clocks"]').trigger('click')
    expect(wrapper.find('[aria-label="危机时钟"]').text()).toContain('教团暗杀倒计时')
    wrapper.unmount()
  })

  test('KP 耗时和步数只对管理员显示，普通玩家看不到引擎指标', async () => {
    const timed = { id: 9, role: 'assistant', kind: 'kp', status: 'done', content: '门开了。', floor: 1, director_elapsed_ms: 60900, director_steps: 1 }
    const player = await setup({ extraMessages: [timed] })
    expect(player.wrapper.text()).toContain('门开了。')
    expect(player.wrapper.text()).not.toMatch(/\d+(\.\d+)?s · \d+ 步/)
    expect(player.wrapper.text()).not.toContain('/步')
    player.wrapper.unmount()

    document.body.innerHTML = ''
    FakeWS.all = []
    const admin = await setup({ extraMessages: [timed], asAdmin: true })
    expect(admin.wrapper.text()).toContain('60.9s · 1 步 · 均 60.9s/步')
    admin.wrapper.unmount()
  })

  test('勾选推荐行动并提交：按表单发送，成功后收成「已提交」摘要', async () => {
    const { wrapper, fetchCalls } = await setup()
    const option = wrapper.findAll('button').find((b) => b.text().includes('绕到后院'))!
    await option.trigger('click')
    const textarea = wrapper.find('textarea')
    await textarea.setValue('小心点')
    const submit = wrapper.findAll('button').find((b) => b.text() === '提交')!
    expect(submit.attributes('disabled')).toBeUndefined()
    await submit.trigger('click')
    await flushPromises()

    expect(fetchCalls).toContainEqual({ method: 'POST', path: '/api/sessions/7/chat' })
    expect(wrapper.text()).toContain('已提交你的行动')
    expect(wrapper.text()).toContain('绕到后院')
    expect(wrapper.find('textarea').exists()).toBe(false)
    wrapper.unmount()
  })

  test('没有选择也没有输入时提交按钮不可用', async () => {
    const { wrapper } = await setup()
    const submit = wrapper.findAll('button').find((b) => b.text() === '提交')!
    expect(submit.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  test('问题卡紧跟在点名消息后面，提交后的摘要也留在原位，不在底部状态栏里', async () => {
    const { wrapper } = await setup()
    const log = wrapper.find('[role="log"]')
    const askArticle = log.findAll('article').find((a) => a.text().includes('点名：爱丽丝'))!
    expect(askArticle).toBeDefined()
    const card = log.find('section[aria-label="KP 点名"]')
    expect(card.exists()).toBe(true)
    expect(askArticle.element.nextElementSibling).toBe(card.element)
    expect(card.text()).not.toContain('你要怎么做？')
    expect(wrapper.find('[role="status"]').element.parentElement?.querySelector('section')).toBeNull()

    await wrapper.findAll('button').find((b) => b.text().includes('推门进去'))!.trigger('click')
    await wrapper.findAll('button').find((b) => b.text() === '提交')!.trigger('click')
    await flushPromises()
    const done = log.findAll('section').find((s) => s.text().includes('已提交你的行动'))!
    expect(askArticle.element.nextElementSibling).toBe(done.element)
    wrapper.unmount()
  })

  test('没被点名时没有输入框；房主在 KP 空闲时能看到继续按钮', async () => {
    const kpSpotlight = { user_ids: [], names: [], question: '', kp_floor: true, asked_me: false, stop: '' }
    const { wrapper } = await setup({ hostId: 2, spotlight: kpSpotlight })
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.text()).toContain('让 KP 继续')
    wrapper.unmount()
  })

  test('管理员被点名时也能看到继续按钮', async () => {
    const { wrapper } = await setup({ hostId: 1, asAdmin: true })
    expect(wrapper.find('section[aria-label="KP 点名"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('让 KP 继续')
    wrapper.unmount()
  })

  test('非房主看不到继续按钮；非房主非管理员在头部看不到结束按钮', async () => {
    const { wrapper } = await setup({ hostId: 1 })
    expect(wrapper.text()).not.toContain('让 KP 继续')
    expect(wrapper.text()).not.toContain('结束')
    wrapper.unmount()
  })

  test('房主点结束：金额写进确认框，取消则不请求', async () => {
    const { wrapper, fetchCalls } = await setup({ hostId: 2 })
    const ui = useUiStore()
    const end = wrapper.findAll('header button').find((b) => b.text() === '结束')!
    await end.trigger('click')
    expect(ui.confirmState?.message).toBe(
      '确认提前结束游戏？将消耗200金币。手动结束不会触发 AI 评分与技能成长，仅结算基础金币与释放人物卡。',
    )
    ui.resolveConfirm(false)
    await flushPromises()
    expect(fetchCalls.some((c) => c.path.endsWith('/end'))).toBe(false)
    wrapper.unmount()
  })

  test('调查员列表能打开并标出自己', async () => {
    const { wrapper } = await setup()
    const ui = useUiStore()
    const list = wrapper.findAll('header button').find((b) => b.text() === '调查员')!
    await list.trigger('click')
    expect(document.body.textContent).toContain('调查员状态')
    expect(document.body.textContent).toContain('YOU')
    expect(ui.toasts).toHaveLength(0)
    wrapper.unmount()
  })

  test('点「我的角色」打开人物卡详情弹窗，关闭后收起', async () => {
    const { wrapper, fetchCalls } = await setup()
    const mine = wrapper.findAll('header button').find((b) => b.text() === '我的角色')!
    await mine.trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('记者')
    expect(fetchCalls).toContainEqual({ method: 'GET', path: '/api/characters/5' })
    expect(document.body.textContent).toContain('最新的怀表')
    const close = [...document.body.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === '关闭')
    expect(close).toBeTruthy()
    close!.click()
    await flushPromises()
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    wrapper.unmount()
  })

  test('生成失败的叙事和配图只显示服务端给出的原因，不再叠一层前缀', async () => {
    const { wrapper } = await setup({
      extraMessages: [
        { id: 4, role: 'assistant', kind: 'writer', status: 'failed', content: '', floor: 1, meta: { error: '叙事生成失败' } },
        { id: 5, role: 'assistant', kind: 'image', status: 'failed', content: '', floor: 1, meta: { error: '配图生成失败' } },
        { id: 6, role: 'assistant', kind: 'writer', status: 'failed', content: '', floor: 1 },
      ],
    })
    const text = wrapper.text()
    expect(text).not.toContain('叙事生成失败：')
    expect(text).not.toContain('配图失败：')
    expect(text.match(/叙事生成失败/g)).toHaveLength(2)
    expect(text.match(/配图生成失败/g)).toHaveLength(1)
    wrapper.unmount()
  })

  test('房主在大厅态房间里没带人物卡：「开始」禁用，可以直接从房间里打开加入弹窗', async () => {
    const { wrapper } = await setup({ hostId: 2, players: [], status: 'lobby' })
    const buttons = () => wrapper.findAll('header button')
    expect(buttons().find((b) => b.text() === '开始')!.attributes('disabled')).toBeDefined()

    await buttons().find((b) => b.text() === '带人物卡加入')!.trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('加入房间')
    expect(document.body.textContent).toContain('雾港旧宅')
    wrapper.unmount()
  })

  test('房主已带人物卡时「开始」可点，且不再显示加入入口', async () => {
    const { wrapper } = await setup({ hostId: 2, status: 'lobby' })
    const texts = wrapper.findAll('header button').map((b) => b.text())
    expect(texts).not.toContain('带人物卡加入')
    expect(wrapper.findAll('header button').find((b) => b.text() === '开始')!.attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  // NOTE: AI 陪玩占一个玩家席位，身份只看 user.role === 'bot'；AI 的卡来自卡池，不是房主自己的卡
  const AI_PLAYER = {
    user_id: 9,
    user: { username: 'AI陪玩', role: 'bot' },
    character_card: { id: 31, name: '陪玩医生', occupation: '医生', stats: { hp: 10, max_hp: 10, san: 60, max_san: 60, mp: 8, max_mp: 8 } },
  }
  const HUMAN_PLAYER = {
    user_id: 2,
    user: { username: 'alice', role: 'user' },
    character_card: { id: 5, name: '爱丽丝', occupation: '记者', stats: { hp: 9, max_hp: 10, san: 40, max_san: 50, mp: 8, max_mp: 8 } },
  }
  const sideButton = (wrapper: Awaited<ReturnType<typeof setup>>['wrapper'], text: string) => wrapper.find('aside').findAll('button').find((b) => b.text() === text)

  test('AI 陪玩：房间里只有 AI 时「开始」禁用并写明原因，侧栏 AI 行带徽标，房主能移除', async () => {
    const { wrapper } = await setup({ hostId: 2, status: 'lobby', players: [AI_PLAYER] })
    const start = wrapper.findAll('header button').find((b) => b.text() === '开始')!
    expect(start.attributes('disabled')).toBeDefined()
    expect(start.attributes('title')).toBe('房间里只有 AI 陪玩，至少需要一名真人玩家才能开始')
    const side = wrapper.find('aside')
    expect(side.text()).toContain('陪玩医生')
    expect(side.find('[title="AI 陪玩"]').exists()).toBe(true)
    expect(sideButton(wrapper, '移除 AI')).toBeDefined()
    expect(sideButton(wrapper, '添加 AI 陪玩')).toBeUndefined()
    wrapper.unmount()
  })

  test('AI 陪玩：有真人时「开始」可点；没有 AI 且未满员时房主能添加，点开后从卡池拉卡', async () => {
    const { wrapper, fetchCalls } = await setup({ hostId: 2, status: 'lobby', maxPlayers: 4, players: [HUMAN_PLAYER] })
    expect(wrapper.findAll('header button').find((b) => b.text() === '开始')!.attributes('disabled')).toBeUndefined()
    expect(sideButton(wrapper, '移除 AI')).toBeUndefined()
    expect(wrapper.find('aside [title="AI 陪玩"]').exists()).toBe(false)
    await sideButton(wrapper, '添加 AI 陪玩')!.trigger('click')
    await flushPromises()
    expect(fetchCalls).toContainEqual({ method: 'GET', path: '/api/sessions/7/companion-cards' })
    expect(document.body.textContent).toContain('陪玩医生')
    wrapper.unmount()
  })

  test('AI 陪玩：房间已满不能添加；非房主看得到 AI 徽标但没有增删入口', async () => {
    const full = await setup({ hostId: 2, status: 'lobby', maxPlayers: 1, players: [HUMAN_PLAYER] })
    expect(sideButton(full.wrapper, '添加 AI 陪玩')).toBeUndefined()
    full.wrapper.unmount()

    const guest = await setup({ hostId: 1, status: 'lobby', maxPlayers: 4, players: [HUMAN_PLAYER, AI_PLAYER] })
    expect(guest.wrapper.find('aside [title="AI 陪玩"]').exists()).toBe(true)
    expect(sideButton(guest.wrapper, '添加 AI 陪玩')).toBeUndefined()
    expect(sideButton(guest.wrapper, '移除 AI')).toBeUndefined()
    guest.wrapper.unmount()
  })

  test('AI 陪玩：游戏开始后不再有增删入口', async () => {
    const { wrapper } = await setup({ hostId: 2, status: 'playing', maxPlayers: 4, players: [HUMAN_PLAYER, AI_PLAYER] })
    expect(sideButton(wrapper, '移除 AI')).toBeUndefined()
    expect(sideButton(wrapper, '添加 AI 陪玩')).toBeUndefined()
    expect(wrapper.find('aside [title="AI 陪玩"]').exists()).toBe(true)
    wrapper.unmount()
  })

  test('AI 陪玩：移除要先确认，取消不请求；确认后 DELETE 并刷新房间', async () => {
    const { wrapper, fetchCalls } = await setup({ hostId: 2, status: 'lobby', players: [HUMAN_PLAYER, AI_PLAYER] })
    const ui = useUiStore()
    await sideButton(wrapper, '移除 AI')!.trigger('click')
    expect(ui.confirmState).toMatchObject({ danger: true, confirmText: '移除' })
    ui.resolveConfirm(false)
    await flushPromises()
    expect(fetchCalls.some((c) => c.method === 'DELETE')).toBe(false)

    const refreshesBefore = fetchCalls.filter((c) => c.path === '/api/sessions/7').length
    await sideButton(wrapper, '移除 AI')!.trigger('click')
    ui.resolveConfirm(true)
    await flushPromises()
    expect(fetchCalls).toContainEqual({ method: 'DELETE', path: '/api/sessions/7/companion' })
    expect(fetchCalls.filter((c) => c.path === '/api/sessions/7').length).toBeGreaterThan(refreshesBefore)
    expect(ui.toasts.at(-1)?.message).toBe('已移除 AI 陪玩')
    wrapper.unmount()
  })

  test('房主没带人物卡进房：调查员列表给出空状态，点「我的角色」提示怎么加入', async () => {
    const { wrapper } = await setup({ hostId: 2, players: [] })
    const ui = useUiStore()
    await wrapper.findAll('header button').find((b) => b.text() === '调查员')!.trigger('click')
    expect(document.body.textContent).toContain('还没有调查员加入')
    const close = [...document.body.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === '关闭')!
    close.click()
    await flushPromises()

    await wrapper.findAll('header button').find((b) => b.text() === '我的角色')!.trigger('click')
    expect(ui.toasts.some((t) => t.message.includes('没有以调查员身份参与'))).toBe(true)
    wrapper.unmount()
  })

  test('右侧栏：人物卡带立绘时在姓名左侧显示小立绘，没有时不渲染图片', async () => {
    const withPortrait = await setup({
      players: [{ user_id: 2, user: { username: 'alice' }, character_card: { id: 5, name: '爱丽丝', occupation: '记者', portrait_hash: 'AB12CD', stats: { hp: 9, max_hp: 10, san: 40, max_san: 50, mp: 8, max_mp: 8 } } }],
    })
    const img = withPortrait.wrapper.find('aside img')
    expect(img.exists()).toBe(true)
    expect(img.attributes('src')).toBe('/api/portraits/ab12cd')
    withPortrait.wrapper.unmount()

    const without = await setup()
    expect(without.wrapper.find('aside img').exists()).toBe(false)
    without.wrapper.unmount()
  })

  test('详情弹窗生成立绘成功后刷新房间，右侧栏拿到新立绘', async () => {
    const { wrapper, fetchCalls, session } = await setup()
    useAuthStore().user!.coins = 500
    expect(wrapper.find('aside img').exists()).toBe(false)
    await wrapper.findAll('header button').find((b) => b.text() === '我的角色')!.trigger('click')
    await flushPromises()

    const generate = [...document.body.querySelectorAll('button')].find((b) => b.textContent?.includes('生成立绘')) as HTMLButtonElement
    generate.click()
    await flushPromises()
    ;(session.players[0]!.character_card as Record<string, unknown>).portrait_hash = 'ff00aa'
    const sessionFetches = () => fetchCalls.filter((c) => c.path === '/api/sessions/7').length
    const before = sessionFetches()
    useUiStore().resolveConfirm(true)
    await flushPromises()

    expect(fetchCalls).toContainEqual({ method: 'POST', path: '/api/characters/5/portrait' })
    expect(sessionFetches()).toBeGreaterThan(before)
    expect(wrapper.find('aside img').attributes('src')).toBe('/api/portraits/ff00aa')
    wrapper.unmount()
  })

  test('writer 正文里的配图占位：画完的显示图片，待画的显示占位，illustration 帧到达后替换，点图片打开预览', async () => {
    const hashA = 'ab'.repeat(32)
    const hashB = 'cd'.repeat(32)
    const { wrapper, ws } = await setup({
      extraMessages: [
        {
          id: 4,
          role: 'assistant',
          kind: 'writer',
          status: 'done',
          floor: 1,
          content: '门开了。\n\n![门廊](illust://1)\n\n![远景](illust://2)\n\n![失败的](illust://3)',
          meta: { illustrations: [{ slot: 1, status: 'done', hash: hashA }, { slot: 3, status: 'failed' }] },
        },
      ],
    })
    const prose = wrapper.find('.prose-md')
    expect(prose.findAll('img').map((i) => i.attributes('src'))).toStrictEqual([`/api/images/${hashA}`])
    expect(prose.findAll('.illust-pending')).toHaveLength(1)
    expect(prose.text()).toContain('配图生成中…')
    expect(prose.text()).not.toContain('illust://')

    ws.recv(4, 'illustration', { message_id: 4, slot: 2, status: 'done', hash: hashB })
    await flushPromises()
    expect(wrapper.find('.prose-md').findAll('img').map((i) => i.attributes('src'))).toStrictEqual([`/api/images/${hashA}`, `/api/images/${hashB}`])
    expect(wrapper.find('.prose-md .illust-pending').exists()).toBe(false)

    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    await wrapper.findAll('.prose-md img')[1]!.trigger('click')
    const shown = document.body.querySelector('[role="dialog"] img')
    expect(shown?.getAttribute('src')).toBe(`/api/images/${hashB}`)
    wrapper.unmount()
  })

  test('管理员可以把 writer 正文切成原始 markdown 调试，再切回渲染；普通玩家没有这个按钮', async () => {
    const hash = 'ab'.repeat(32)
    const writer = {
      id: 4,
      role: 'assistant',
      kind: 'writer',
      status: 'done',
      floor: 1,
      content: '门开了。\n\n![门廊](illust://1)\n\n他走进去。',
      meta: { illustrations: [{ slot: 1, status: 'done', hash }] },
    }
    const admin = await setup({ asAdmin: true, extraMessages: [writer] })
    const toggle = () => admin.wrapper.findAll('button').find((b) => b.text().includes('源码'))!
    expect(admin.wrapper.find('[data-testid="writer-raw"]').exists()).toBe(false)
    expect(admin.wrapper.find('.prose-md img').exists()).toBe(true)

    await toggle().trigger('click')
    const raw = admin.wrapper.find('[data-testid="writer-raw"]')
    expect(raw.text()).toBe('门开了。\n\n![门廊](illust://1)\n\n他走进去。')
    expect(admin.wrapper.find('.prose-md img').exists()).toBe(false)
    expect(toggle().attributes('aria-pressed')).toBe('true')

    await toggle().trigger('click')
    expect(admin.wrapper.find('[data-testid="writer-raw"]').exists()).toBe(false)
    expect(admin.wrapper.find('.prose-md img').exists()).toBe(true)
    admin.wrapper.unmount()

    const player = await setup({ extraMessages: [writer] })
    expect(player.wrapper.findAll('button').some((b) => b.text().includes('源码'))).toBe(false)
    player.wrapper.unmount()
  })

  test('管理员可以按需查看 writer 的导演指令：只请求一次、能和源码同时打开、空指令有提示；普通玩家没有按钮也不请求', async () => {
    const writer = { id: 4, role: 'assistant', kind: 'writer', status: 'done', floor: 1, content: '门开了。', meta: { call_id: 'call_w1' } }
    const legacy = { id: 5, role: 'assistant', kind: 'writer', status: 'done', floor: 1, content: '旧正文。', meta: {} }
    const directionCalls = (calls: Array<{ method: string; path: string }>, id: number) =>
      calls.filter((c) => c.method === 'GET' && c.path === `/api/admin/messages/${id}/direction`).length

    const admin = await setup({ asAdmin: true, extraMessages: [writer, legacy] })
    const toggle = (label: string, idx: number) => admin.wrapper.findAll('button').filter((b) => b.text().includes(label))[idx]!
    expect(admin.wrapper.find('[data-testid="writer-direction"]').exists()).toBe(false)
    expect(directionCalls(admin.fetchCalls, 4)).toBe(0)

    await toggle('指令', 0).trigger('click')
    await flushPromises()
    expect(admin.wrapper.find('[data-testid="writer-direction"]').text()).toBe('描述门廊，强调霉味')
    expect(admin.wrapper.text()).toContain('call_w1')
    expect(admin.wrapper.find('.prose-md').exists()).toBe(true)
    expect(toggle('指令', 0).attributes('aria-pressed')).toBe('true')
    expect(directionCalls(admin.fetchCalls, 4)).toBe(1)

    await toggle('源码', 0).trigger('click')
    expect(admin.wrapper.find('[data-testid="writer-direction"]').exists()).toBe(true)
    expect(admin.wrapper.find('[data-testid="writer-raw"]').exists()).toBe(true)

    await toggle('指令', 0).trigger('click')
    expect(admin.wrapper.find('[data-testid="writer-direction"]').exists()).toBe(false)
    await toggle('指令', 0).trigger('click')
    await flushPromises()
    expect(admin.wrapper.find('[data-testid="writer-direction"]').text()).toBe('描述门廊，强调霉味')
    expect(directionCalls(admin.fetchCalls, 4)).toBe(1)

    await toggle('指令', 1).trigger('click')
    await flushPromises()
    expect(admin.wrapper.text()).toContain('没有记录指令')
    expect(admin.wrapper.text()).toContain('自动补写')
    admin.wrapper.unmount()

    const player = await setup({ extraMessages: [writer] })
    expect(player.wrapper.findAll('button').some((b) => b.text().includes('指令'))).toBe(false)
    expect(player.fetchCalls.some((c) => c.path.startsWith('/api/admin/'))).toBe(false)
    player.wrapper.unmount()
  })

  test('流式中的 writer 正文：配图占位显示「配图生成中…」，没写完的图片语法不显示原始标记', async () => {
    const { wrapper, ws } = await setup({
      extraMessages: [{ id: 4, role: 'assistant', kind: 'writer', status: 'pending', floor: 1, content: '', meta: {} }],
    })
    ws.recv(0, 'writer_token', { message_id: 4, offset: 0, text: '门开了。\n\n![门廊](illust://1)\n\n他走进去![远景](ill' })
    await flushPromises()
    const prose = wrapper.find('.prose-md')
    expect(prose.findAll('.illust-pending')).toHaveLength(1)
    expect(prose.text()).toContain('他走进去')
    expect(prose.text()).not.toContain('![')
    expect(prose.text()).not.toContain('illust')
    wrapper.unmount()
  })

  test('离开页面时关闭 WebSocket', async () => {
    const { wrapper, ws } = await setup()
    expect(ws.closed).toBe(false)
    wrapper.unmount()
    expect(ws.closed).toBe(true)
  })
})

// NOTE: 分类器结果内嵌在消息流里（仅管理员）：玩家消息下是 chip，还没成为玩家消息的输入分类是独立行；非管理员的页面里没有任何相关内容，也不请求
describe('GameView 分类器内嵌', () => {
  type Wrapper = Awaited<ReturnType<typeof setup>>['wrapper']
  type Calls = Array<{ method: string; path: string }>

  const adminCalls = (calls: Calls) => calls.filter((c) => c.path.startsWith('/api/admin/sessions/'))
  const messageCalls = (calls: Calls) => calls.filter((c) => c.path === '/api/sessions/7/messages').length

  function articleOf(wrapper: Wrapper, text: string) {
    const found = wrapper.find('[role="log"]').findAll('article').find((a) => a.text().includes(text))
    if (!found) throw new Error(`消息流里没有包含「${text}」的消息`)
    return found
  }

  // NOTE: 按 DOM 顺序列出消息流里的消息、问题卡和孤儿行，用来断言孤儿行落在哪里。
  // jsdom 的逗号选择器结果按选择器分组、不保证文档顺序，所以要自己按文档位置排序
  function streamOrder(wrapper: Wrapper): string[] {
    const log = wrapper.find('[role="log"]').element
    const nodes = [...log.querySelectorAll('article, section[aria-label="KP 点名"], [data-testid="classifier-orphan"]')]
    nodes.sort((a, b) => (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1))
    return nodes.map((el) => {
      if (el.matches('[data-testid="classifier-orphan"]')) return `orphan:${el.querySelector('[data-testid="orphan-subject"]')?.textContent}`
      if (el.tagName === 'SECTION') return 'ask-card'
      if (el.querySelector('ul[aria-label="KP 结算"]')) return 'event'
      const text = el.textContent ?? ''
      if (text.includes('雨夜里')) return 'kp'
      if (text.includes('点名：爱丽丝')) return 'ask'
      return `player:${/行动(\d+)/.exec(text)?.[1]}`
    })
  }

  const chipKinds = (article: ReturnType<typeof articleOf>) => article.findAll('[data-testid="chip-kind"]').map((n) => n.text())

  test('管理员：消息加载后请求一次历史（limit=500），玩家消息下出现 chip，头部不再有「分类器」入口', async () => {
    const admin = await setup({
      asAdmin: true,
      spotlight: SPOT_KP,
      extraMessages: [playerMessage(4, 2)],
      classifierLogs: [classifierLog(1, { floor: 2 }), routeLog(2, { floor: 2 })],
    })
    expect(adminCalls(admin.fetchCalls)).toStrictEqual([{ method: 'GET', path: CLASSIFIER_PATH }])
    const paths = admin.fetchCalls.map((c) => c.path)
    expect(paths.indexOf(CLASSIFIER_PATH)).toBeGreaterThan(paths.indexOf('/api/sessions/7/messages'))

    const article = articleOf(admin.wrapper, '行动4')
    const chips = article.find('[data-testid="classifier-chips"]')
    expect(chips.exists()).toBe(true)
    expect(chips.findAll('[data-testid="chip-kind"]').map((n) => n.text())).toStrictEqual(['输入分类', 'NSFW 路由'])
    expect(chips.findAll('[data-testid="chip-decision"]').map((n) => n.text())).toStrictEqual(['放行', '走 NSFW Director'])
    expect(chips.findAll('[data-testid="chip-score"]').map((n) => n.text())).toStrictEqual(['0.10 / 0.80', '0.72 / 0.50'])
    expect(admin.wrapper.find('[data-testid="classifier-orphan"]').exists()).toBe(false)

    // NOTE: 自己的消息，chip 行靠右对齐，和气泡一致；点开后详情在 chip 下面，末尾有去系统设置的链接
    expect(chips.find('div.flex').classes()).toContain('ml-auto')
    const first = chips.findAll('[data-testid="classifier-chip"]')[0]
    expect(first.attributes('aria-expanded')).toBe('false')
    expect(article.find('[data-testid="classifier-detail"]').exists()).toBe(false)
    await first.trigger('click')
    expect(first.attributes('aria-expanded')).toBe('true')
    const detail = article.find('[data-testid="classifier-detail"]')
    expect(detail.exists()).toBe(true)
    expect(detail.attributes('id')).toBe(first.attributes('aria-controls'))
    const link = detail.find('a[href="/admin/settings"]')
    expect(link.attributes('target')).toBe('_blank')

    expect(admin.wrapper.findAll('header button').some((b) => b.text().includes('分类器'))).toBe(false)
    await admin.wrapper.find('button[aria-label="更多操作"]').trigger('click')
    expect(admin.wrapper.findAll('[role="menuitem"]').some((b) => b.text().includes('分类器'))).toBe(false)
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    admin.wrapper.unmount()
  })

  test('管理员：同一周期多名玩家时，输入分类各挂各的消息，路由 chip 只挂在该周期最后一条玩家消息', async () => {
    const admin = await setup({
      asAdmin: true,
      spotlight: SPOT_KP,
      extraMessages: [playerMessage(4, 2), playerMessage(5, 2, { user_id: 3, username: 'bob' })],
      classifierLogs: [
        classifierLog(1, { floor: 2, user_id: 2 }),
        classifierLog(2, { floor: 2, user_id: 3, subject: '鲍勃' }),
        routeLog(3, { floor: 2 }),
        routeLog(4, { floor: 9 }),
      ],
    })
    expect(chipKinds(articleOf(admin.wrapper, '行动4'))).toStrictEqual(['输入分类'])
    expect(chipKinds(articleOf(admin.wrapper, '行动5'))).toStrictEqual(['输入分类', 'NSFW 路由'])
    // NOTE: 别人的消息不靠右；没有对应玩家消息的周期（9）不展示
    expect(articleOf(admin.wrapper, '行动5').find('[data-testid="classifier-chips"] div.flex').classes()).not.toContain('ml-auto')
    expect(admin.wrapper.findAll('[data-testid="classifier-chip"]')).toHaveLength(3)
    admin.wrapper.unmount()
  })

  test('非管理员：不请求分类器接口，页面里没有任何分类器内容，即使收到了实时帧', async () => {
    const player = await setup({
      spotlight: SPOT_KP,
      extraMessages: [playerMessage(4, 2)],
      classifierLogs: [classifierLog(1, { floor: 2 }), classifierLog(2, { floor: 0 })],
    })
    player.ws.recv(0, 'classifier_log', { log: classifierLog(3, { floor: 2 }) })
    player.ws.recv(0, 'classifier_log', { log: classifierLog(4, { floor: 0 }) })
    await flushPromises()

    expect(player.fetchCalls.some((c) => c.path.startsWith('/api/admin/'))).toBe(false)
    expect(articleOf(player.wrapper, '行动4').exists()).toBe(true)
    expect(player.wrapper.find('[data-testid^="classifier"]').exists()).toBe(false)
    expect(player.wrapper.html()).not.toContain('classifier')
    expect(player.wrapper.text()).not.toContain('输入分类')
    expect(player.wrapper.findAll('header button').some((b) => b.text().includes('分类器'))).toBe(false)
    player.wrapper.unmount()
  })

  test('孤儿行（输入分类 floor 为 0）按 created_at 放在紧挨着的前一条消息之后；前一条是点名消息时放在问题卡之后', async () => {
    const admin = await setup({
      asAdmin: true,
      classifierLogs: [
        classifierLog(1, { subject: '最早', created_at: '2026-11-28T09:59:00' }),
        classifierLog(2, { subject: '开场后', created_at: '2026-11-28T10:00:10' }),
        classifierLog(3, { subject: '问题卡后', created_at: '2026-11-28T10:00:25', decision: 'blocked', score: 0.9 }),
        classifierLog(4, { subject: '结算后', created_at: '2026-11-28T10:00:40' }),
        classifierLog(5, { subject: '同刻', created_at: '2026-11-28T10:00:10' }),
      ],
    })
    expect(streamOrder(admin.wrapper)).toStrictEqual([
      'orphan:最早',
      'kp',
      'orphan:开场后',
      'orphan:同刻',
      'ask',
      'ask-card',
      'orphan:问题卡后',
      'event',
      'orphan:结算后',
    ])

    // NOTE: 孤儿行不在任何消息里面；展示提交者、输入摘要、判定 chip 和说明
    const rows = admin.wrapper.findAll('[data-testid="classifier-orphan"]')
    expect(rows.every((r) => r.element.closest('article') === null)).toBe(true)
    const blocked = rows.find((r) => r.text().includes('问题卡后'))!
    expect(blocked.find('[data-testid="orphan-input"]').text()).toBe('「我推门进去」')
    expect(blocked.find('[data-testid="chip-decision"]').text()).toBe('已拒收')
    expect(blocked.find('[data-testid="orphan-note"]').text()).toBe('已拒收，未进入对局')
    const waiting = rows.find((r) => r.text().includes('结算后'))!
    expect(waiting.find('[data-testid="chip-decision"]').text()).toBe('放行')
    expect(waiting.find('[data-testid="orphan-note"]').text()).toBe('尚未成为行动（等待其他玩家，或被后一次提交取代）')

    await waiting.find('[data-testid="classifier-chip"]').trigger('click')
    expect(waiting.find('[data-testid="classifier-detail"]').exists()).toBe(true)
    expect(rows.find((r) => r.text().includes('同刻'))!.find('[data-testid="classifier-detail"]').exists()).toBe(false)
    admin.wrapper.unmount()
  })

  test('问题卡在消息流末尾时，挂在最后一条消息后面的孤儿行排在问题卡之后', async () => {
    // NOTE: 点名消息之后已有玩家发言，问题卡找不到锚点，放到消息流末尾（紧跟最后一条消息）
    const admin = await setup({
      asAdmin: true,
      extraMessages: [playerMessage(4, 2)],
      classifierLogs: [
        classifierLog(1, { subject: '玩家前', created_at: '2026-11-28T10:00:45' }),
        classifierLog(2, { subject: '最末', created_at: '2026-11-28T10:30:00' }),
      ],
    })
    expect(streamOrder(admin.wrapper)).toStrictEqual(['kp', 'ask', 'event', 'orphan:玩家前', 'player:4', 'ask-card', 'orphan:最末'])
    admin.wrapper.unmount()
  })

  test('小说阅读模式下，孤儿行和 chip 都隐藏，切回来后恢复', async () => {
    const admin = await setup({
      asAdmin: true,
      spotlight: SPOT_KP,
      extraMessages: [playerMessage(4, 2)],
      classifierLogs: [classifierLog(1, { floor: 2 }), classifierLog(2, { subject: '孤儿', created_at: '2026-11-28T10:00:25' })],
    })
    const rows = () => admin.wrapper.findAll('[data-testid="classifier-orphan"]').length
    const chips = () => admin.wrapper.findAll('[data-testid="classifier-chip"]').length
    expect([rows(), chips()]).toStrictEqual([1, 2])

    const toggle = admin.wrapper.find('button[aria-label="小说阅读模式"]')
    await toggle.trigger('click')
    expect([rows(), chips()]).toStrictEqual([0, 0])
    expect(admin.wrapper.find('ul[aria-label="KP 结算"]').exists()).toBe(false)
    expect(articleOf(admin.wrapper, '行动4').exists()).toBe(true)

    await toggle.trigger('click')
    expect([rows(), chips()]).toStrictEqual([1, 2])
    admin.wrapper.unmount()
  })

  test('实时帧用同一个 id 改写 floor：chip 从孤儿行移到玩家消息下面', async () => {
    const admin = await setup({
      asAdmin: true,
      spotlight: SPOT_KP,
      extraMessages: [playerMessage(4, 2)],
      classifierLogs: [classifierLog(1, { subject: '爱丽丝', created_at: '2026-11-28T10:00:50' })],
    })
    expect(streamOrder(admin.wrapper)).toStrictEqual(['kp', 'ask', 'event', 'orphan:爱丽丝', 'player:4'])
    expect(chipKinds(articleOf(admin.wrapper, '行动4'))).toStrictEqual([])

    admin.ws.recv(0, 'classifier_log', { log: classifierLog(1, { subject: '爱丽丝', created_at: '2026-11-28T10:00:50', floor: 2 }) })
    await flushPromises()
    expect(streamOrder(admin.wrapper)).toStrictEqual(['kp', 'ask', 'event', 'player:4'])
    expect(chipKinds(articleOf(admin.wrapper, '行动4'))).toStrictEqual(['输入分类'])

    // NOTE: 同一周期的路由记录随后到达，挂在同一条消息上；重复到达同一帧不会多出 chip
    admin.ws.recv(0, 'classifier_log', { log: routeLog(2, { floor: 2 }) })
    admin.ws.recv(0, 'classifier_log', { log: routeLog(2, { floor: 2 }) })
    await flushPromises()
    expect(chipKinds(articleOf(admin.wrapper, '行动4'))).toStrictEqual(['输入分类', 'NSFW 路由'])
    expect(adminCalls(admin.fetchCalls)).toHaveLength(1)
    admin.wrapper.unmount()
  })

  test('历史响应晚于实时事件到达时，以实时事件为准，不会把新的 floor 盖回旧值', async () => {
    let release!: () => void
    const gate = new Promise<void>((resolve) => (release = resolve))
    const admin = await setup({
      asAdmin: true,
      spotlight: SPOT_KP,
      extraMessages: [playerMessage(4, 2)],
      classifierRespond: async () => {
        await gate
        return json({ logs: [classifierLog(1, { subject: '旧快照', floor: 0, created_at: '2026-11-28T10:00:50' }), classifierLog(2, { subject: '只在历史里', created_at: '2026-11-28T10:00:55' })] })
      },
    })
    // NOTE: 请求还没返回时，实时帧先把 1 号记录改成了周期 2 的行动
    admin.ws.recv(0, 'classifier_log', { log: classifierLog(1, { subject: '最新', floor: 2, created_at: '2026-11-28T10:00:50' }) })
    await flushPromises()
    release()
    await flushPromises()

    expect(streamOrder(admin.wrapper)).toStrictEqual(['kp', 'ask', 'event', 'orphan:只在历史里', 'player:4'])
    expect(chipKinds(articleOf(admin.wrapper, '行动4'))).toStrictEqual(['输入分类'])
    admin.wrapper.unmount()
  })

  test('重连后重新拉快照时再请求一次历史，补回断线期间丢掉的临时事件', async () => {
    const admin = await setup({
      asAdmin: true,
      spotlight: SPOT_KP,
      extraMessages: [playerMessage(4, 2)],
      classifierLogs: [classifierLog(1, { subject: '爱丽丝', created_at: '2026-11-28T10:00:50' })],
    })
    expect(adminCalls(admin.fetchCalls)).toHaveLength(1)
    expect(streamOrder(admin.wrapper)).toContain('orphan:爱丽丝')

    // NOTE: 断线期间服务端把输入记成了周期 2 的行动并写了路由记录，这两个实时帧都丢了
    admin.classifier.logs = [classifierLog(1, { subject: '爱丽丝', created_at: '2026-11-28T10:00:50', floor: 2 }), routeLog(2, { floor: 2 })]
    admin.ws.recv(9, 'message_new', { message: playerMessage(9, 3) })
    const ws2 = FakeWS.all[FakeWS.all.length - 1]
    expect(ws2).not.toBe(admin.ws)
    expect(adminCalls(admin.fetchCalls)).toHaveLength(1)
    ws2.recv(0, 'hello', { epoch: 'E1', seq: 9 })
    await flushPromises()

    expect(adminCalls(admin.fetchCalls)).toStrictEqual([
      { method: 'GET', path: CLASSIFIER_PATH },
      { method: 'GET', path: CLASSIFIER_PATH },
    ])
    expect(streamOrder(admin.wrapper)).toStrictEqual(['kp', 'ask', 'event', 'player:4'])
    expect(chipKinds(articleOf(admin.wrapper, '行动4'))).toStrictEqual(['输入分类', 'NSFW 路由'])
    admin.wrapper.unmount()
  })

  test('无缝重连（不需要快照）也会在 hello 之后重拉一次历史', async () => {
    const admin = await setup({
      asAdmin: true,
      spotlight: SPOT_KP,
      extraMessages: [playerMessage(4, 2)],
      classifierLogs: [classifierLog(1, { subject: '爱丽丝', created_at: '2026-11-28T10:00:50' })],
    })
    const snapshots = messageCalls(admin.fetchCalls)
    admin.classifier.logs = [classifierLog(1, { subject: '爱丽丝', created_at: '2026-11-28T10:00:50', floor: 2 })]

    // NOTE: 连接断开后立刻重连（页面重新可见），带着游标，服务端不要求快照
    admin.ws.onclose?.()
    document.dispatchEvent(new Event('visibilitychange'))
    const ws2 = FakeWS.all[FakeWS.all.length - 1]
    expect(ws2).not.toBe(admin.ws)
    expect(ws2.url).toContain('since=')
    ws2.recv(0, 'hello', { epoch: 'E1', seq: 3 })
    await flushPromises()

    expect(messageCalls(admin.fetchCalls)).toBe(snapshots)
    expect(adminCalls(admin.fetchCalls)).toHaveLength(2)
    expect(streamOrder(admin.wrapper)).toStrictEqual(['kp', 'ask', 'event', 'player:4'])
    expect(chipKinds(articleOf(admin.wrapper, '行动4'))).toStrictEqual(['输入分类'])
    admin.wrapper.unmount()
  })

  test('历史请求失败只在控制台和轻提示里说明：整页照常，同一个房间只提示一次', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const admin = await setup({
      asAdmin: true,
      spotlight: SPOT_KP,
      extraMessages: [playerMessage(4, 2)],
      classifierRespond: async () => json({ error: '服务异常' }, 500),
    })
    const toasts = () => useUiStore().toasts.map((t) => t.message)
    expect(toasts()).toHaveLength(1)
    expect(toasts()[0]).toContain('分类器记录加载失败')
    expect(warn).toHaveBeenCalledTimes(1)
    expect(articleOf(admin.wrapper, '行动4').exists()).toBe(true)

    admin.ws.recv(9, 'message_new', { message: playerMessage(9, 3) })
    FakeWS.all[FakeWS.all.length - 1].recv(0, 'hello', { epoch: 'E1', seq: 9 })
    await flushPromises()
    expect(adminCalls(admin.fetchCalls)).toHaveLength(2)
    expect(warn).toHaveBeenCalledTimes(2)
    expect(toasts()).toHaveLength(1)
    expect(articleOf(admin.wrapper, '行动4').exists()).toBe(true)
    admin.wrapper.unmount()
  })

  test('切换房间后迟到的历史响应被丢弃：失败也不会给新房间弹提示', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    let release!: () => void
    const gate = new Promise<void>((resolve) => (release = resolve))
    const admin = await setup({
      asAdmin: true,
      classifierRespond: async () => {
        await gate
        return json({ error: '服务异常' }, 500)
      },
    })
    await admin.router.push('/sessions/8')
    await flushPromises()
    expect(admin.wrapper.text()).toContain('另一个房间')

    release()
    await flushPromises()
    expect(warn).not.toHaveBeenCalled()
    expect(useUiStore().toasts).toStrictEqual([])
    admin.wrapper.unmount()
  })
})

// NOTE: jsdom 没有布局，scrollHeight / clientHeight 恒为 0；这里用可变的假高度模拟内容增长，滚动位置由用户滚动事件驱动
describe('GameView 滚动跟随', () => {
  const KP_FLOOR = { user_ids: [], names: [], question: '', kp_floor: true, asked_me: false, stop: '' }
  const kpMessage2 = { id: 10, role: 'assistant', kind: 'kp', status: 'done', content: '风停了。', floor: 2 }
  const askMessage2 = {
    id: 10,
    role: 'assistant',
    kind: 'ask',
    status: 'done',
    content: '接下来怎么办？',
    floor: 2,
    meta: { options: [], ask: { user_ids: [2], names: ['爱丽丝'] } },
  }
  const writerPending = { id: 10, role: 'assistant', kind: 'writer', status: 'pending', content: '', floor: 2, meta: {} }

  async function enterRoom() {
    const geo = { height: 2000 }
    vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(() => geo.height)
    vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(500)
    const frames: FrameRequestCallback[] = []
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => frames.push(cb))
    const room = await setup({ spotlight: KP_FLOOR })
    const log = room.wrapper.find('[role="log"]').element as HTMLElement
    const userScroll = (top: number) => {
      log.scrollTop = top
      log.dispatchEvent(new Event('scroll'))
    }
    // NOTE: 先等界面更新，再放行动画帧，和浏览器里「帧回调在渲染之后」的次序一致
    const settle = async () => {
      await flushPromises()
      for (const cb of frames.splice(0)) cb(0)
    }
    return { ...room, geo, log, userScroll, settle }
  }

  test('首次进房的快照把页面放到最新消息处', async () => {
    const { wrapper, log } = await enterRoom()
    expect(log.scrollTop).toBe(2000)
    wrapper.unmount()
  })

  test('玩家向上翻阅时，点名问题卡出现不会把页面拉到底部', async () => {
    const { wrapper, ws, geo, log, userScroll, settle } = await enterRoom()
    userScroll(300)

    ws.recv(4, 'message_new', { message: askMessage2 })
    ws.recv(5, 'spotlight', { user_ids: [2], names: ['爱丽丝'], question: '接下来怎么办？', kp_floor: false, stop: '' })
    geo.height = 2400
    await settle()

    expect(wrapper.find('section[aria-label="KP 点名"]').exists()).toBe(true)
    expect(log.scrollTop).toBe(300)
    wrapper.unmount()
  })

  test('停在底部的玩家，点名问题卡在点名消息之后才出现，页面仍跟到卡片底部', async () => {
    const { wrapper, ws, geo, log, settle } = await enterRoom()

    ws.recv(4, 'message_new', { message: askMessage2 })
    await settle()
    ws.recv(5, 'spotlight', { user_ids: [2], names: ['爱丽丝'], question: '接下来怎么办？', kp_floor: false, stop: '' })
    geo.height = 2300
    await settle()

    expect(wrapper.find('section[aria-label="KP 点名"]').exists()).toBe(true)
    expect(log.scrollTop).toBe(2300)
    wrapper.unmount()
  })

  test('玩家向上翻阅时，旁白流式生成不会拉回底部；回到底部后恢复跟随', async () => {
    const { wrapper, ws, geo, log, userScroll, settle } = await enterRoom()
    userScroll(300)

    ws.recv(4, 'message_new', { message: writerPending })
    ws.recv(0, 'writer_token', { message_id: 10, offset: 0, text: '门开了。' })
    geo.height = 2200
    await settle()
    expect(log.scrollTop).toBe(300)

    userScroll(1700)
    ws.recv(0, 'writer_token', { message_id: 10, offset: 4, text: '他走进去。' })
    geo.height = 2400
    await settle()
    expect(log.scrollTop).toBe(2400)
    wrapper.unmount()
  })

  test('停在底部的玩家，新消息一次长高很多也继续跟随', async () => {
    const { wrapper, ws, geo, log, settle } = await enterRoom()

    ws.recv(4, 'message_new', { message: kpMessage2 })
    geo.height = 3000
    await settle()

    expect(log.scrollTop).toBe(3000)
    wrapper.unmount()
  })

  test('重连后重新拉取的快照不会把正在翻阅的玩家拉到底部', async () => {
    const { wrapper, ws, geo, log, userScroll, settle } = await enterRoom()
    userScroll(300)

    // NOTE: 序号出现缺口，控制器会丢弃游标、重连并重新拉一次快照
    ws.recv(9, 'message_new', { message: kpMessage2 })
    const ws2 = FakeWS.all[FakeWS.all.length - 1]
    expect(ws2).not.toBe(ws)
    ws2.recv(0, 'hello', { epoch: 'E1', seq: 9 })
    geo.height = 2400
    await settle()

    expect(log.scrollTop).toBe(300)
    wrapper.unmount()
  })
})
