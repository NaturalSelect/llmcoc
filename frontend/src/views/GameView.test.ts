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

const kpMessage = {
  id: 1,
  role: 'assistant',
  kind: 'kp',
  status: 'done',
  content: '雨夜里，门廊的灯忽明忽暗。',
  floor: 1,
}

const askMessage = {
  id: 2,
  role: 'assistant',
  kind: 'ask',
  status: 'done',
  content: '你要怎么做？',
  floor: 1,
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

interface Setup {
  hostId?: number
  spotlight?: Record<string, unknown>
  extraMessages?: Array<Record<string, unknown>>
  players?: Array<Record<string, unknown>>
}

async function setup({ hostId = 1, spotlight = SPOT_ME, extraMessages = [], players }: Setup = {}) {
  const fetchCalls: Array<{ method: string; path: string }> = []
  const session = {
    id: 7,
    name: '雾港旧宅',
    status: 'playing',
    created_by: hostId,
    scenario: { name: '旧宅疑云' },
    players: players ?? [{ user_id: 2, user: { username: 'alice' }, character_card: { id: 5, name: '爱丽丝', occupation: '记者', stats: { hp: 9, max_hp: 10, san: 40, max_san: 50, mp: 8, max_mp: 8 } } }],
  }
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method || 'GET'
      fetchCalls.push({ method, path })
      if (path === '/api/sessions/7') return json(session)
      if (path === '/api/sessions/7/messages') return json([kpMessage, eventMessage, askMessage, ...extraMessages])
      if (path === '/api/sessions/7/chat-status') {
        return json({ kp_running: false, submitted: false, floor: null, spotlight, waiting: { pending: 1, total: 1, submitted_names: [], pending_names: ['爱丽丝'] } })
      }
      if (path === '/api/sessions/7/chat') return json({}, 202)
      if (path === '/api/characters/5') return json({ id: 5, name: '爱丽丝', occupation: '记者', inventory: [{ name: '最新的怀表', count: 1, type: 'misc' }], stats: { hp: 3, max_hp: 10, san: 20, max_san: 50, mp: 8, max_mp: 8 } })
      return json({})
    }),
  )
  vi.stubGlobal('WebSocket', FakeWS)

  const pinia = createPinia()
  setActivePinia(pinia)
  localStorage.setItem('coc_token', 'tok')
  const auth = useAuthStore()
  auth.user = { id: 2, username: 'alice', role: 'user' }
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
  return { wrapper, ws, router, fetchCalls }
}

beforeEach(() => {
  FakeWS.all = []
  document.body.innerHTML = ''
  localStorage.clear()
})

afterEach(() => {
  vi.unstubAllGlobals()
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
    expect(ui.confirmState?.message).toBe('确认结束游戏？将消耗200金币，系统将进行成长评估。')
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

  test('离开页面时关闭 WebSocket', async () => {
    const { wrapper, ws } = await setup()
    expect(ws.closed).toBe(false)
    wrapper.unmount()
    expect(ws.closed).toBe(true)
  })
})
