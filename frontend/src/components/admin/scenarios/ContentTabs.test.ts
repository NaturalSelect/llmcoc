// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import AnnouncementsTab from '@/views/admin/AnnouncementsTab.vue'
import ScenariosTab from '@/views/admin/ScenariosTab.vue'
import SettingsTab from '@/views/admin/SettingsTab.vue'
import ShopTab from '@/views/admin/ShopTab.vue'
import { useUiStore } from '@/stores/ui'

type Call = { method: string; url: string; body: unknown }

// NOTE: 按 "METHOD path" 路由的假 fetch；路径不含查询串，未登记的请求返回 404 以便暴露遗漏
function stubApi(routes: Record<string, unknown>) {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit = {}) => {
      const method = init.method || 'GET'
      calls.push({ method, url, body: init.body ? JSON.parse(init.body as string) : undefined })
      const key = `${method} ${url.split('?')[0]}`
      if (!(key in routes)) return { ok: false, status: 404, json: async () => ({ error: '未登记 ' + key }) } as Response
      return { ok: true, status: 200, json: async () => routes[key] } as Response
    }),
  )
  return calls
}

const page = (items: unknown[], extra: Record<string, unknown> = {}) => ({ items, page: 1, page_size: 20, total: items.length, total_pages: 1, ...extra })

let ui: ReturnType<typeof useUiStore>

beforeEach(() => {
  setActivePinia(createPinia())
  ui = useUiStore()
  document.body.innerHTML = ''
})

afterEach(() => {
  vi.unstubAllGlobals()
})

function buttonByText(root: { findAll: (s: string) => Array<{ text: () => string; trigger: (e: string) => Promise<void> }> }, text: string) {
  const btn = root.findAll('button').find((b) => b.text() === text)
  if (!btn) throw new Error('找不到按钮：' + text)
  return btn
}

describe('SettingsTab', () => {
  const settingsRows = [
    { key: 'require_invite_code', value: 'true' },
    { key: 'allow_nsfw', value: 'false' },
    { key: 'initial_coins', value: '0' },
    { key: 'balance_rules', value: '规则' },
  ]
  const routes = {
    'GET /api/admin/config/settings': settingsRows,
    'GET /api/admin/invite-codes': page([]),
    'PUT /api/admin/config/settings/require_invite_code': {},
    'PUT /api/admin/config/settings/initial_coins': {},
    'PUT /api/admin/config/settings/lawyer_cache_min_similarity': {},
    'PUT /api/admin/config/settings/balance_rules': {},
    'POST /api/admin/sessions/end-all': { message: '已强制结束 2 个房间' },
  }

  test('加载后回填开关与数值；开关切换走 PUT 并提示「设置已更新」', async () => {
    const calls = stubApi(routes)
    const wrapper = mount(SettingsTab)
    await flushPromises()

    const switches = wrapper.findAll('[role="switch"]')
    expect(switches.map((s) => s.attributes('aria-checked'))).toStrictEqual(['true', 'false'])
    expect((wrapper.find('#cost-initial_coins').element as HTMLInputElement).value).toBe('0')

    await switches[0].trigger('click')
    await flushPromises()
    expect(calls.at(-1)).toMatchObject({ method: 'PUT', url: '/api/admin/config/settings/require_invite_code', body: { value: 'false' } })
    expect(wrapper.findAll('[role="switch"]')[0].attributes('aria-checked')).toBe('false')
    expect(ui.toasts.at(-1)).toMatchObject({ message: '设置已更新', type: 'success' })
  })

  test('数值保存：非法值提示「请输入有效的正整数」且不发请求；合法值 PUT 字符串', async () => {
    const calls = stubApi(routes)
    const wrapper = mount(SettingsTab)
    await flushPromises()
    const before = calls.length

    const input = wrapper.find('#cost-initial_coins')
    await input.setValue('-5')
    await wrapper.findAll('button').filter((b) => b.text() === '保存')[0].trigger('click')
    expect(calls.length).toBe(before)
    expect(ui.toasts.at(-1)).toMatchObject({ message: '请输入有效的正整数', type: 'error' })

    await input.setValue('300')
    await wrapper.findAll('button').filter((b) => b.text() === '保存')[0].trigger('click')
    await flushPromises()
    expect(calls.at(-1)).toMatchObject({ method: 'PUT', url: '/api/admin/config/settings/initial_coins', body: { value: '300' } })
  })

  test('Lawyer 缓存相似度阈值：缺省回填 0.3；越界在前端拦下，合法值 PUT 字符串', async () => {
    const calls = stubApi(routes)
    const wrapper = mount(SettingsTab)
    await flushPromises()
    const before = calls.length

    const input = wrapper.find('#lawyer-cache-min-similarity')
    expect((input.element as HTMLInputElement).value).toBe('0.3')
    const saveBtn = input.element.closest('div.flex')!.parentElement!.querySelector('button')!
    const save = async () => {
      saveBtn.click()
      await flushPromises()
    }

    await input.setValue('1.5')
    await save()
    expect(calls.length).toBe(before)
    expect(ui.toasts.at(-1)).toMatchObject({ message: '相似度阈值需为 -1 到 1 之间的数字', type: 'error' })

    await input.setValue('')
    await save()
    expect(calls.length).toBe(before)

    await input.setValue('0.45')
    await save()
    expect(calls.at(-1)).toMatchObject({ method: 'PUT', url: '/api/admin/config/settings/lawyer_cache_min_similarity', body: { value: '0.45' } })
    expect(ui.toasts.at(-1)).toMatchObject({ message: '相似度阈值已保存', type: 'success' })
  })

  test('平衡规则超过 2000 字符被拦下；合法时保存并提示', async () => {
    const calls = stubApi(routes)
    const wrapper = mount(SettingsTab)
    await flushPromises()
    const before = calls.length
    const textarea = wrapper.find('textarea')
    const saveBtn = wrapper.findAll('button').filter((b) => b.text() === '保存').at(-1)!

    await textarea.setValue('x'.repeat(2001))
    expect(wrapper.text()).toContain('2001/2000')
    await saveBtn.trigger('click')
    expect(calls.length).toBe(before)
    expect(ui.toasts.at(-1)).toMatchObject({ message: '平衡调整规则不能超过 2000 个字符', type: 'error' })

    await textarea.setValue('新规则')
    await saveBtn.trigger('click')
    await flushPromises()
    expect(calls.at(-1)).toMatchObject({ method: 'PUT', url: '/api/admin/config/settings/balance_rules', body: { value: '新规则' } })
    expect(ui.toasts.at(-1)).toMatchObject({ message: '平衡调整规则已保存', type: 'success' })
  })

  test('一键结束：确认后 POST，并用后端返回的 message 提示；取消则不发请求', async () => {
    const calls = stubApi(routes)
    const wrapper = mount(SettingsTab)
    await flushPromises()
    const before = calls.length

    await buttonByText(wrapper, '一键结束').trigger('click')
    expect(ui.confirmState?.message).toBe('确认强制结束所有运行中的房间？此操作不可逆，不会向玩家收取结束游戏费用。')
    expect(ui.confirmState).toMatchObject({ danger: true, confirmText: '一键结束' })
    ui.resolveConfirm(false)
    await flushPromises()
    expect(calls.length).toBe(before)

    await buttonByText(wrapper, '一键结束').trigger('click')
    ui.resolveConfirm(true)
    await flushPromises()
    expect(calls.at(-1)).toMatchObject({ method: 'POST', url: '/api/admin/sessions/end-all' })
    expect(ui.toasts.at(-1)).toMatchObject({ message: '已强制结束 2 个房间', type: 'success' })
  })
})

describe('AnnouncementsTab', () => {
  const routes = {
    'GET /api/admin/announcements': page([
      { id: 7, title: '维护通知', content: 'c', level: 'warning', is_pinned: true, is_active: true, starts_at: null, ends_at: null, created_at: '2026-01-01T00:00:00Z' },
    ]),
    'POST /api/admin/announcements': {},
    'DELETE /api/admin/announcements/7': {},
  }

  test('列表展示等级、置顶与状态；空标题保存被拦下，合法时 POST 并关闭弹窗', async () => {
    const calls = stubApi(routes)
    const wrapper = mount(AnnouncementsTab, { attachTo: document.body })
    await flushPromises()
    expect(wrapper.text()).toContain('维护通知')
    expect(wrapper.text()).toContain('警告')
    expect(wrapper.text()).toContain('置顶')
    expect(wrapper.text()).toContain('展示中')

    await buttonByText(wrapper, '+ 新建公告').trigger('click')
    await flushPromises()
    const dialog = () => document.body.querySelector('[role="dialog"]') as HTMLElement
    expect(dialog().getAttribute('aria-label')).toBe('新建公告')

    const submit = () => dialog().querySelector('button[type="submit"]') as HTMLButtonElement
    submit().click()
    await flushPromises()
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
    expect(ui.toasts.at(-1)).toMatchObject({ message: '标题不能为空', type: 'error' })

    const title = dialog().querySelector('input') as HTMLInputElement
    title.value = '新公告'
    title.dispatchEvent(new Event('input'))
    submit().click()
    await flushPromises()
    const post = calls.find((c) => c.method === 'POST')!
    expect(post.body).toMatchObject({ title: '新公告', level: 'normal', is_pinned: false, is_active: true, starts_at: null, ends_at: null })
    expect(ui.toasts.at(-1)).toMatchObject({ message: '公告已创建', type: 'success' })
    expect(dialog()).toBeNull()
    wrapper.unmount()
  })

  test('删除：确认文案正确，确认后 DELETE 并提示「已删除」', async () => {
    const calls = stubApi(routes)
    const wrapper = mount(AnnouncementsTab)
    await flushPromises()
    await buttonByText(wrapper, '删除').trigger('click')
    expect(ui.confirmState).toMatchObject({ message: '确认删除此公告？', danger: true, confirmText: '删除' })
    ui.resolveConfirm(true)
    await flushPromises()
    expect(calls.some((c) => c.method === 'DELETE' && c.url === '/api/admin/announcements/7')).toBe(true)
    expect(ui.toasts.at(-1)).toMatchObject({ message: '已删除', type: 'success' })
  })
})

describe('ShopTab', () => {
  const routes = {
    'GET /api/shop/items': page([{ id: 3, name: '卡槽', description: '多一个槽位', item_type: 'card_slot', price: 100, value: 1 }]),
    'POST /api/admin/shop/items': {},
    'DELETE /api/admin/shop/items/3': {},
  }

  test('名称为空时创建静默无动作；填写后 POST 并重置表单、回到第 1 页刷新', async () => {
    const calls = stubApi(routes)
    const wrapper = mount(ShopTab)
    await flushPromises()
    expect(wrapper.text()).toContain('价格: 100 金币')
    expect(wrapper.text()).toContain('效果值: 1')

    const before = calls.length
    await buttonByText(wrapper, '创建商品').trigger('click')
    await flushPromises()
    expect(calls.length).toBe(before)
    expect(ui.toasts).toStrictEqual([])

    const name = wrapper.find('input[placeholder="商品名称 *"]')
    await name.setValue('金币包')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    const post = calls.find((c) => c.method === 'POST')!
    expect(post.body).toMatchObject({ name: '金币包', item_type: 'card_slot', price: 0, value: 1, is_active: true })
    expect(ui.toasts.at(-1)).toMatchObject({ message: '商品已创建', type: 'success' })
    expect((name.element as HTMLInputElement).value).toBe('')
    expect(calls.at(-1)).toMatchObject({ method: 'GET', url: '/api/shop/items?page=1&page_size=20' })
  })

  test('删除商品：确认文案带商品名', async () => {
    stubApi(routes)
    const wrapper = mount(ShopTab)
    await flushPromises()
    await buttonByText(wrapper, '删除').trigger('click')
    expect(ui.confirmState?.message).toBe('确认删除商品“卡槽”？删除后将从商城下架。')
    ui.resolveConfirm(false)
  })
})

describe('ScenariosTab', () => {
  const routes = {
    'GET /api/admin/scenarios': page([{ id: 5, name: '雨夜', difficulty: 'hard', min_players: 2, max_players: 0, author: '佚名' }]),
    'DELETE /api/scenarios/5': {},
    'DELETE /api/scenarios': {},
    'GET /api/admin/scenarios/5/generation-log': {
      scenario_id: 5, scenario_name: '雨夜', has_log: true, log_text: '对话记录正文', has_story: false, story_document: '',
    },
  }

  test('列表行副标题：难度 · 人数范围（上限缺省为 4）· 作者', async () => {
    stubApi(routes)
    const wrapper = mount(ScenariosTab)
    await flushPromises()
    expect(wrapper.text()).toContain('雨夜')
    expect(wrapper.text()).toContain('困难 · 2-4人 · 佚名')
    expect(wrapper.text()).toContain('共 1 个 · 第 1 / 1 页')
  })

  test('LLM 记录：打开弹窗加载后显示「没有故事原文」提示，切到对话日志页显示正文', async () => {
    const calls = stubApi(routes)
    const wrapper = mount(ScenariosTab, { attachTo: document.body })
    await flushPromises()
    await buttonByText(wrapper, 'LLM记录').trigger('click')
    await flushPromises()
    expect(calls.at(-1)).toMatchObject({ method: 'GET', url: '/api/admin/scenarios/5/generation-log' })
    const dialog = document.body.querySelector('[role="dialog"]') as HTMLElement
    expect(dialog.textContent).toContain('该模组没有保存故事原文，可能是改动前生成的旧模组。')
    const dialogTab = Array.from(dialog.querySelectorAll('[role="tab"]')).find((t) => t.textContent?.trim() === 'LLM对话日志') as HTMLElement
    dialogTab.click()
    await flushPromises()
    expect(dialog.textContent).toContain('对话记录正文')
    wrapper.unmount()
  })

  test('清空所有模组：确认文案与 DELETE /api/scenarios', async () => {
    const calls = stubApi(routes)
    const wrapper = mount(ScenariosTab)
    await flushPromises()
    await buttonByText(wrapper, '清空所有模组').trigger('click')
    expect(ui.confirmState).toMatchObject({
      message: '确认清空所有模组？此操作不可逆，将删除全部已有模组。',
      danger: true,
      confirmText: '清空',
    })
    ui.resolveConfirm(true)
    await flushPromises()
    expect(calls.some((c) => c.method === 'DELETE' && c.url === '/api/scenarios')).toBe(true)
    expect(ui.toasts.at(-1)).toMatchObject({ message: '模组已清空', type: 'success' })
  })
})
