// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import AgentsTab from '@/views/admin/AgentsTab.vue'
import CacheTab from '@/views/admin/CacheTab.vue'
import ProvidersTab from '@/views/admin/ProvidersTab.vue'
import UsersTab from '@/views/admin/UsersTab.vue'
import { useUiStore } from '@/stores/ui'

// NOTE: 冒烟测试：用假 fetch 按「方法 + 路径」应答，验证四个 tab 能渲染、请求体和确认流程与旧前端一致
interface Call {
  method: string
  path: string
  body: unknown
}
let calls: Call[] = []
let routes: Record<string, unknown> = {}

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ method, path, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      const key = `${method} ${path}`
      const hit = key in routes ? routes[key] : routes[`${method} ${path.split('?')[0]}`]
      if (hit === undefined) return { ok: false, status: 404, json: async () => ({ error: '无路由 ' + key }) }
      return { ok: true, status: 200, json: async () => hit }
    }),
  )
}

const user = (over: Record<string, unknown> = {}) => ({ id: 1, username: 'alice', email: 'a@x.io', role: 'user', is_banned: false, coins: 50, ...over })
const agentRow = (role: string, over: Record<string, unknown> = {}) => ({
  id: 3,
  role,
  provider_config_id: 2,
  model_name: 'gpt-4o',
  max_tokens: 800,
  temperature: 0.7,
  disable_temperature: false,
  image_via_chat: false,
  diffusion_mode: false,
  context_window: 0,
  with_jailbreak: false,
  system_prompt: '',
  thinking_level: 'high',
  thinking_budget_tokens: 0,
  is_active: true,
  ...over,
})

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  document.body.innerHTML = ''
  calls = []
  routes = {}
  vi.unstubAllGlobals()
  stubFetch()
})

afterEach(() => {
  vi.useRealTimers()
})

const btn = (root: { findAll: (s: string) => { text: () => string }[] }, text: string) => {
  const hit = root.findAll('button').find((b) => b.text() === text)
  if (!hit) throw new Error('找不到按钮：' + text)
  return hit as unknown as { trigger: (e: string) => Promise<void> }
}

describe('UsersTab', () => {
  test('渲染用户并按旧流程封号：带原因的确认框 → PUT ban → 已封号 toast', async () => {
    routes['GET /api/admin/users'] = { items: [user()], page: 1, page_size: 20, total: 1, total_pages: 1 }
    routes['PUT /api/admin/users/1/ban'] = { message: '已封号' }
    const w = mount(UsersTab, { attachTo: document.body })
    await flushPromises()
    expect(w.text()).toContain('#1 alice')
    expect(w.text()).toContain('共 1 个 · 第 1 / 1 页')
    expect(calls[0]).toMatchObject({ method: 'GET', path: '/api/admin/users?page=1&page_size=20' })

    const ui = useUiStore()
    const pending = btn(w, '封号').trigger('click')
    await pending
    expect(ui.confirmState).toMatchObject({ message: '封号 alice？', withInput: true, danger: true, confirmText: '封号', inputPlaceholder: '封号原因（可留空）' })
    ui.confirmState!.inputValue = '刷屏'
    ui.resolveConfirm(true)
    await flushPromises()
    expect(calls.at(-1)).toMatchObject({ method: 'PUT', path: '/api/admin/users/1/ban', body: { reason: '刷屏' } })
    expect(ui.toasts.at(-1)).toMatchObject({ message: '已封号', type: 'success' })
    expect(w.text()).toContain('已封')
    w.unmount()
  })

  test('充值：请求体原样提交，成功 toast 带用户名与数量，随后刷新列表', async () => {
    routes['GET /api/admin/users'] = { items: [], page: 1, page_size: 20, total: 0, total_pages: 1 }
    routes['POST /api/admin/recharge'] = { message: '充值成功', user: user() }
    const w = mount(UsersTab, { attachTo: document.body })
    await flushPromises()
    expect(w.text()).toContain('暂无用户')
    const inputs = w.findAll('input')
    await inputs[0]!.setValue('1')
    await btn(w, '确认充值').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.path === '/api/admin/recharge')?.body).toStrictEqual({ user_id: 1, amount: 100, note: '' })
    expect(useUiStore().toasts.at(-1)?.message).toBe('充值成功：alice +100 金币')
    expect(calls.filter((c) => c.path.startsWith('/api/admin/users?')).length).toBe(2)
    w.unmount()
  })

  test('每页条数切换后回到第 1 页并按新条数请求', async () => {
    routes['GET /api/admin/users'] = { items: [user()], page: 2, page_size: 20, total: 40, total_pages: 2 }
    const w = mount(UsersTab, { attachTo: document.body })
    await flushPromises()
    await w.find('select').setValue(50)
    await flushPromises()
    expect(calls.at(-1)?.path).toBe('/api/admin/users?page=1&page_size=50')
    w.unmount()
  })
})

describe('ProvidersTab', () => {
  test('空列表提示；新建提供商提交默认表单并刷新', async () => {
    routes['GET /api/admin/config/providers'] = []
    routes['POST /api/admin/config/providers'] = { id: 1 }
    const w = mount(ProvidersTab, { attachTo: document.body })
    await flushPromises()
    expect(w.text()).toContain('暂无提供商，请创建。')
    await btn(w, '+ 新建提供商').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('新建 LLM 提供商')
    const name = document.body.querySelector<HTMLInputElement>('input[placeholder="显示名称 *"]')!
    name.value = 'my'
    name.dispatchEvent(new Event('input'))
    document.body.querySelector<HTMLButtonElement>('button[type="submit"]')!.click()
    await flushPromises()
    expect(calls.find((c) => c.method === 'POST')).toMatchObject({
      path: '/api/admin/config/providers',
      body: { name: 'my', provider: 'openai', base_url: '', api_key: '', is_active: true },
    })
    expect(useUiStore().toasts.at(-1)?.message).toBe('提供商已创建')
    w.unmount()
  })

  test('展示 Key 状态与默认端点文案；编辑时 Key 占位符为「保留原值」', async () => {
    routes['GET /api/admin/config/providers'] = [{ id: 5, name: 'p1', provider: 'anthropic', base_url: '', api_key_set: true, is_active: false }]
    const w = mount(ProvidersTab, { attachTo: document.body })
    await flushPromises()
    expect(w.text()).toContain('（OpenAI 默认端点）')
    expect(w.text()).toContain('API Key 已配置')
    expect(w.text()).toContain('已停用')
    await btn(w, '编辑').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('input[placeholder="新 API Key（留空保留原值）"]')).not.toBeNull()
    w.unmount()
  })
})

describe('AgentsTab', () => {
  test('库里缺行的角色补默认行（共 14 张卡），保存请求体与旧前端字段一致', async () => {
    routes['GET /api/admin/config/providers'] = [{ id: 2, name: 'prov', provider: 'openai', base_url: '', api_key_set: true, is_active: true }]
    routes['GET /api/admin/config/agents'] = [agentRow('director', { system_prompt: 'hi' }), agentRow('scripter')]
    routes['PUT /api/admin/config/agents/director'] = {}
    const w = mount(AgentsTab, { attachTo: document.body })
    await flushPromises()
    expect(w.findAll('article')).toHaveLength(14)
    expect(w.text()).toContain('🎬 Director')
    expect(w.text()).not.toContain('scripter')
    const first = w.findAll('article')[0]!
    await btn(first, '保存配置').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.method === 'PUT')).toMatchObject({
      path: '/api/admin/config/agents/director',
      body: {
        provider_config_id: 2,
        model_name: 'gpt-4o',
        max_tokens: 800,
        context_window: 0,
        temperature: 0.7,
        disable_temperature: false,
        image_via_chat: false,
        diffusion_mode: false,
        with_jailbreak: false,
        system_prompt: 'hi',
        thinking_level: 'high',
        thinking_budget_tokens: 0,
        is_active: true,
      },
    })
    expect(useUiStore().toasts.at(-1)?.message).toBe('🎬 Director 配置已保存')
    w.unmount()
  })

  test('测试模型：未选提供商/未填模型名走旧校验文案，不发请求', async () => {
    routes['GET /api/admin/config/providers'] = []
    routes['GET /api/admin/config/agents'] = []
    const w = mount(AgentsTab, { attachTo: document.body })
    await flushPromises()
    const before = calls.length
    await btn(w.findAll('article')[0]!, '测试模型').trigger('click')
    expect(useUiStore().toasts.at(-1)).toMatchObject({ message: '请先选择 Provider', type: 'error' })
    expect(calls.length).toBe(before)
    w.unmount()
  })

  test('画图角色才显示 Chat 接口 / 扩散模式开关，两者互斥禁用', async () => {
    routes['GET /api/admin/config/providers'] = []
    routes['GET /api/admin/config/agents'] = []
    const w = mount(AgentsTab, { attachTo: document.body })
    await flushPromises()
    const cards = w.findAll('article')
    const painter = cards.find((c) => c.text().includes('🎨 Painter'))!
    const director = cards[0]!
    expect(painter.text()).toContain('画图走 Chat 接口')
    expect(director.text()).not.toContain('画图走 Chat 接口')
    const boxes = painter.findAll('input[type="checkbox"]')
    const viaChat = boxes[1]!
    const diffusion = boxes[2]!
    await viaChat.setValue(true)
    expect((diffusion.element as HTMLInputElement).disabled).toBe(true)
    w.unmount()
  })
})

describe('CacheTab', () => {
  const stats = { full_hits: 6, partial_hits: 2, misses: 2, entries: 3, used_bytes: 5242880, max_bytes: 4294967296 }
  function cacheRoutes() {
    routes['GET /api/admin/cache/stats'] = stats
    routes['GET /api/admin/cache/keys'] = { keys: ['k1'], total: 1, page: 1, page_size: 20, total_pages: 1 }
    routes['GET /api/admin/llm/stats'] = { overall: { key: 'overall', count: 4, avg_ms: 10.4, max_ms: 20, err_count: 1, cache_hit_rate: 0.25 }, by_role: null, by_model: [] }
  }

  test('渲染统计、命中率与容量文案，只发一次初始请求（无轮询）', async () => {
    vi.useFakeTimers()
    cacheRoutes()
    const w = mount(CacheTab, { attachTo: document.body })
    await flushPromises()
    expect(w.text()).toContain('80.0%')
    expect(w.text()).toContain('3 条 · 5.0 MB / 4 GB')
    expect(w.text()).toContain('10 ms')
    expect(w.text()).toContain('25.0%')
    const n = calls.length
    vi.advanceTimersByTime(10 * 60 * 1000)
    await flushPromises()
    expect(calls.length).toBe(n)
    w.unmount()
  })

  test('查看条目 → 详情；删除条目走确认框并带 key 参数', async () => {
    cacheRoutes()
    routes['GET /api/admin/cache/entry?key=k1'] = { key: 'k1', value: '裁定文本', size: 12 }
    routes['DELETE /api/admin/cache/entry?key=k1'] = { message: 'ok' }
    const w = mount(CacheTab, { attachTo: document.body })
    await flushPromises()
    await btn(w, '查看').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('裁定文本')
    expect(w.text()).toContain('大小：12 字节')

    const ui = useUiStore()
    await btn(w, '删除').trigger('click')
    expect(ui.confirmState).toMatchObject({ message: '确认删除缓存条目：k1？', danger: true, confirmText: '删除' })
    ui.resolveConfirm(true)
    await flushPromises()
    expect(calls.some((c) => c.method === 'DELETE' && c.path === '/api/admin/cache/entry?key=k1')).toBe(true)
    expect(ui.toasts.at(-1)?.message).toBe('条目已删除')
    expect(w.text()).not.toContain('裁定文本')
    w.unmount()
  })

  test('会话上下文：空 ID 提示；有结果时展示摘要与最近轮次（倒序）', async () => {
    cacheRoutes()
    routes['GET /api/admin/sessions/42/context'] = {
      agents: [
        {
          agent_key: 'director',
          turns: 4,
          next_seq: 9,
          estimated_runes: 1200,
          recent_stats: [
            { seq: 1, round: 1, calls: 2, prompt_tokens: 100, cache_read_tokens: 50 },
            { seq: 2, round: 1, calls: 1, prompt_tokens: 0, cache_read_tokens: 0, trimmed_turns: 3 },
          ],
        },
      ],
    }
    const w = mount(CacheTab, { attachTo: document.body })
    await flushPromises()
    await btn(w, '查询').trigger('click')
    expect(useUiStore().toasts.at(-1)).toMatchObject({ message: '请输入会话ID', type: 'error' })

    await w.find('input[placeholder="会话ID"]').setValue(' 42 ')
    await btn(w, '查询').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('4 轮 · next_seq=9 · 约 1200 字符')
    const text = w.text()
    expect(text.indexOf('seq=2 round=1')).toBeLessThan(text.indexOf('seq=1 round=1'))
    expect(text).toContain('2次 · prompt 100 · 命中 50.0%')
    expect(text).toContain('1次 · prompt 0 · 命中 0.0% · trim 3')
    w.unmount()
  })
})
