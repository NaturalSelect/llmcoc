// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { useUiStore } from '@/stores/ui'
import CompanionPoolTab from './CompanionPoolTab.vue'

// NOTE: 冒烟测试：假 fetch 按「方法 + 路径」应答（查询串会被忽略），验证卡池页的状态标签、移入/移回的请求体和确认流程
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
      if (hit instanceof Error) return { ok: false, status: 409, json: async () => ({ error: hit.message }) }
      return { ok: true, status: 200, json: async () => hit }
    }),
  )
}

const poolCard = (id: number, over: Record<string, unknown> = {}) => ({
  id,
  name: '卡' + id,
  occupation: '记者',
  age: 28,
  gender: '女',
  portrait_hash: '',
  is_active: true,
  wound_state: 'none',
  hp: 10,
  max_hp: 12,
  san: 50,
  max_san: 60,
  in_session: false,
  ...over,
})
const ownCard = (id: number, over: Record<string, unknown> = {}) => ({
  id,
  name: '我的卡' + id,
  occupation: '律师',
  is_active: true,
  wound_state: 'none',
  in_session: false,
  stats: { hp: 10 },
  ...over,
})

function setup() {
  routes['GET /api/admin/companion-cards'] = [poolCard(5), poolCard(6, { in_session: true }), poolCard(7, { wound_state: 'dead' })]
  routes['GET /api/characters'] = { items: [ownCard(21), ownCard(22, { in_session: true }), ownCard(23, { wound_state: 'dead' })], total_pages: 1 }
}

type Wrapper = ReturnType<typeof mount>
const buttons = (w: Wrapper, text: string) => w.findAll('button').filter((b) => b.text() === text)

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  document.body.innerHTML = ''
  calls = []
  routes = {}
  vi.unstubAllGlobals()
  stubFetch()
  // NOTE: 批量生成面板挂载时会 attach 一条 WebSocket；这里的假连接永不打开，免得走到 jsdom 的真实连接
  vi.stubGlobal(
    'WebSocket',
    class {
      send() {}
      close() {}
    },
  )
})

describe('CompanionPoolTab', () => {
  test('渲染卡池：空闲 / 对局中 / 已死亡或失活三种标签，对局中的卡不能移回', async () => {
    setup()
    const w = mount(CompanionPoolTab, { attachTo: document.body })
    await flushPromises()
    const items = w.findAll('li').map((li) => li.text())
    expect(items).toHaveLength(3)
    expect(items[0]).toContain('卡5')
    expect(items[0]).toContain('空闲')
    expect(items[0]).toContain('记者 · 28 岁 · 女')
    expect(items[1]).toContain('对局中')
    expect(items[2]).toContain('已死亡或失活')
    const back = buttons(w, '移回我名下')
    expect(back.map((b) => (b.element as HTMLButtonElement).disabled)).toStrictEqual([false, true, false])
    w.unmount()
  })

  test('候选只含自己名下可用的卡：对局中和已死亡的不在下拉里', async () => {
    setup()
    const w = mount(CompanionPoolTab, { attachTo: document.body })
    await flushPromises()
    const options = w.findAll('option').map((o) => o.text())
    expect(options).toStrictEqual(['选择人物卡', '我的卡21 (律师)'])
    expect((buttons(w, '从我的卡移入')[0]!.element as HTMLButtonElement).disabled).toBe(true)
    w.unmount()
  })

  test('卡池为空（后端返回 null）时给出空状态，没有可移入的卡时下拉置灰', async () => {
    routes['GET /api/admin/companion-cards'] = null
    routes['GET /api/characters'] = { items: [], total_pages: 1 }
    const w = mount(CompanionPoolTab, { attachTo: document.body })
    await flushPromises()
    expect(w.text()).toContain('卡池里还没有人物卡')
    expect((w.find('select').element as HTMLSelectElement).disabled).toBe(true)
    expect(w.text()).toContain('没有可移入的人物卡')
    w.unmount()
  })

  test('从我的卡移入：确认后 POST card_id，toast 后刷新卡池', async () => {
    setup()
    routes['POST /api/admin/companion-cards'] = { message: '已移入' }
    const w = mount(CompanionPoolTab, { attachTo: document.body })
    await flushPromises()
    await w.find('select').setValue(21)
    const ui = useUiStore()
    await buttons(w, '从我的卡移入')[0]!.trigger('click')
    expect(ui.confirmState).toMatchObject({ confirmText: '移入' })
    expect(ui.confirmState?.message).toContain('我的卡21')
    ui.resolveConfirm(true)
    await flushPromises()
    expect(calls.find((c) => c.method === 'POST')).toMatchObject({ path: '/api/admin/companion-cards', body: { card_id: 21 } })
    expect(ui.toasts.at(-1)).toMatchObject({ message: '已移入', type: 'success' })
    expect(calls.filter((c) => c.path === '/api/admin/companion-cards' && c.method === 'GET')).toHaveLength(2)
    w.unmount()
  })

  test('取消确认框不发请求', async () => {
    setup()
    const w = mount(CompanionPoolTab, { attachTo: document.body })
    await flushPromises()
    await w.find('select').setValue(21)
    const ui = useUiStore()
    await buttons(w, '从我的卡移入')[0]!.trigger('click')
    ui.resolveConfirm(false)
    await flushPromises()
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
    w.unmount()
  })

  test('移回我名下：确认后 POST /return；失败时弹错误 toast 并重新拉取卡池', async () => {
    setup()
    routes['POST /api/admin/companion-cards/5/return'] = new Error('这张卡正在房间里使用')
    const w = mount(CompanionPoolTab, { attachTo: document.body })
    await flushPromises()
    const ui = useUiStore()
    await buttons(w, '移回我名下')[0]!.trigger('click')
    expect(ui.confirmState).toMatchObject({ confirmText: '移回' })
    ui.resolveConfirm(true)
    await flushPromises()
    expect(calls.find((c) => c.method === 'POST')).toMatchObject({ path: '/api/admin/companion-cards/5/return' })
    expect(ui.toasts.at(-1)).toMatchObject({ message: '这张卡正在房间里使用', type: 'error' })
    expect(calls.filter((c) => c.path === '/api/admin/companion-cards' && c.method === 'GET')).toHaveLength(2)
    w.unmount()
  })

  test('对局中的卡点击「移回我名下」不会弹确认框也不发请求', async () => {
    setup()
    const w = mount(CompanionPoolTab, { attachTo: document.body })
    await flushPromises()
    await buttons(w, '移回我名下')[1]!.trigger('click')
    expect(useUiStore().confirmState).toBeNull()
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
    w.unmount()
  })
})
