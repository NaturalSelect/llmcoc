// @vitest-environment jsdom
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import ConfirmHost from '@/components/base/ConfirmHost.vue'
import DashboardView from '@/views/DashboardView.vue'
import { useAuthStore } from '@/stores/auth'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'

interface Call {
  method: string
  path: string
  body: unknown
}

let calls: Call[]
let routes: Record<string, (call: Call) => unknown>

const alive = { id: 1, user_id: 9, name: '艾米', occupation: '记者', age: 28, gender: '女', is_active: true, in_session: false, stats: { str: 50, con: 50, dex: 60, int: 70, hp: 8, max_hp: 10, san: 40, max_san: 60, mp: 5, max_mp: 12 }, skills: { 侦查: 60 }, inventory: ['绷带', '手电筒'], spells: ['束缚术'], backstory: '旧事', appearance: '高挑', traits: '冷静' }
const dead = { id: 2, user_id: 9, name: '鲍勃', occupation: '医生', is_active: false, stats: {} }

function respond(body: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => body } as Response
}

function install() {
  vi.stubGlobal('fetch', async (path: string, init: RequestInit = {}) => {
    const call: Call = { method: init.method as string, path, body: init.body ? JSON.parse(init.body as string) : undefined }
    calls.push(call)
    const key = `${call.method} ${path.split('?')[0]}`
    const handler = routes[key]
    if (!handler) return respond({ error: `unrouted ${key}` }, 500)
    const out = handler(call)
    return out instanceof Response ? out : respond(out)
  })
}

function mountDashboard() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }, { path: '/sessions', name: 'sessions', component: { template: '<div />' } }, { path: '/sessions/:id', name: 'game', component: { template: '<div />' } }] })
  return mount({ components: { DashboardView, ConfirmHost }, template: '<DashboardView /><ConfirmHost />' }, { attachTo: document.body, global: { plugins: [router] } })
}

async function confirmDialog(accept: boolean) {
  const buttons = [...document.body.querySelectorAll('[role="alertdialog"] button')] as HTMLButtonElement[]
  buttons.find((b) => (accept ? b.textContent?.trim() !== '取消' : b.textContent?.trim() === '取消'))!.click()
  await flushPromises()
}

const text = () => document.body.textContent ?? ''
const button = (label: string) => [...document.body.querySelectorAll('button')].find((b) => b.textContent?.trim().includes(label)) as HTMLButtonElement | undefined

let wrapper: VueWrapper

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  document.body.innerHTML = ''
  calls = []
  routes = {
    'GET /api/characters': () => ({ items: [structuredClone(alive)], total_pages: 1 }),
    'GET /api/characters/dead': () => ({ items: [structuredClone(dead)], total_pages: 1 }),
    'GET /api/characters/1': () => structuredClone(alive),
    'GET /api/announcements': () => [{ id: 1, title: '维护通知', content: '**今晚** 维护', level: 'warning', is_pinned: true, created_at: '2026-01-01T00:00:00Z' }],
    'GET /api/sessions': () => ({ items: [{ id: 5, name: '雾中小镇', status: 'lobby', max_players: 4, scenario: { name: '迷雾' }, players: [{}] }] }),
    'GET /api/shop/costs': () => ({ revive_base_cost: 500, regenerate_traits_cost: 30 }),
  }
  install()
  const auth = useAuthStore()
  auth.user = { id: 9, username: 'u', role: 'user', coins: 1000 }
})

afterEach(() => {
  wrapper?.unmount()
  vi.unstubAllGlobals()
})

describe('DashboardView', () => {
  test('加载并渲染人物卡、阵亡卡、公告（Markdown 经净化）和最近房间', async () => {
    wrapper = mountDashboard()
    await flushPromises()
    expect(text()).toContain('艾米')
    expect(text()).toContain('人类 · 记者 · 28岁 · 女')
    expect(text()).toContain('8/10')
    expect(text()).toContain('已阵亡的调查员')
    expect(text()).toContain('复活费用：500 金币')
    expect(text()).toContain('置顶')
    expect(document.body.querySelector('.announcement-md strong')?.textContent).toBe('今晚')
    expect(text()).toContain('雾中小镇')
    expect(text()).toContain('1/4 位调查员')
  })

  test('没有人物卡时显示空状态', async () => {
    routes['GET /api/characters'] = () => ({ items: [], total_pages: 1 })
    wrapper = mountDashboard()
    await flushPromises()
    expect(text()).toContain('还没有调查员档案，创建第一张人物卡开始冒险！')
  })

  test('删除人物卡：取消不发请求；确认后移除并刷新阵亡列表', async () => {
    wrapper = mountDashboard()
    await flushPromises()
    calls = []
    ;(document.body.querySelector('[aria-label="删除人物卡 艾米"]') as HTMLButtonElement).click()
    await nextTick()
    expect(text()).toContain('确认删除此人物卡？')
    await confirmDialog(false)
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)

    routes['DELETE /api/characters/1'] = () => ({ message: 'ok' })
    ;(document.body.querySelector('[aria-label="删除人物卡 艾米"]') as HTMLButtonElement).click()
    await nextTick()
    await confirmDialog(true)
    expect(calls.map((c) => `${c.method} ${c.path.split('?')[0]}`)).toEqual(['DELETE /api/characters/1', 'GET /api/characters/dead'])
    expect(useCharactersStore().characters).toHaveLength(0)
    expect(useUiStore().toasts.map((t) => t.message)).toContain('人物卡已删除')
  })

  test('复活：金币不足时按钮禁用；足够时确认后扣币并移到存活列表', async () => {
    useAuthStore().user!.coins = 100
    wrapper = mountDashboard()
    await flushPromises()
    expect(button('复活')!.disabled).toBe(true)

    useAuthStore().user!.coins = 1000
    await nextTick()
    expect(button('复活')!.disabled).toBe(false)
    routes['POST /api/characters/2/revive'] = () => ({ coins: 500, character_card: { ...dead, is_active: true } })
    button('复活')!.click()
    await nextTick()
    expect(text()).toContain('确认花费 500 金币复活「鲍勃」？')
    await confirmDialog(true)
    expect(useAuthStore().user!.coins).toBe(500)
    expect(useCharactersStore().characters.map((c) => c.name)).toEqual(['艾米', '鲍勃'])
    expect(useCharactersStore().deadCharacters).toHaveLength(0)
    expect(useUiStore().toasts.map((t) => t.message)).toContain('鲍勃 已复活！但部分记忆与装备永久消散……')
  })

  test('彻底删除阵亡人物卡', async () => {
    routes['DELETE /api/characters/2/dead'] = () => ({ message: 'ok' })
    wrapper = mountDashboard()
    await flushPromises()
    button('彻底删除')!.click()
    await nextTick()
    expect(text()).toContain('确认彻底删除「鲍勃」？此操作不可撤销，角色将永久消逝。')
    await confirmDialog(true)
    expect(useCharactersStore().deadCharacters).toHaveLength(0)
    expect(useUiStore().toasts.map((t) => t.message)).toContain('鲍勃 已彻底消逝……')
  })

  test('详情弹窗：重新生成扣币并更新文本；指导语去空格后发送', async () => {
    routes['POST /api/characters/1/regenerate-traits'] = () => ({ traits: '暴躁', coins: 970 })
    wrapper = mountDashboard()
    await flushPromises()
    button('艾米')!.click()
    await nextTick()
    expect(document.body.querySelector('[role="dialog"]')).not.toBeNull()
    expect(text()).toContain('物品栏')
    expect(text()).toContain('重新生成 -30')

    const input = document.body.querySelector('[aria-label="性格特征指导"]') as HTMLInputElement
    input.value = '  毒舌  '
    input.dispatchEvent(new Event('input'))
    calls = []
    button('重新生成 -30')!.click()
    await nextTick()
    expect(text()).toContain('确认花费 30 金币为「艾米」重新生成性格特征？')
    await confirmDialog(true)
    expect(calls[0]).toMatchObject({ method: 'POST', path: '/api/characters/1/regenerate-traits', body: { guidance: '毒舌' } })
    expect(useAuthStore().user!.coins).toBe(970)
    expect(text()).toContain('暴躁')
    expect((useCharactersStore().characters[0] as { traits?: string }).traits).toBe('暴躁')
  })

  test('详情弹窗：丢弃物品请求路径经过编码，并同步 store；in_session 沿用列表值', async () => {
    useCharactersStore().characters = []
    routes['GET /api/characters'] = () => ({ items: [{ ...structuredClone(alive), in_session: true }], total_pages: 1 })
    routes['DELETE /api/characters/1/inventory/%E7%BB%B7%E5%B8%A6'] = () => ({ ...alive, inventory: ['手电筒'], in_session: false })
    wrapper = mountDashboard()
    await flushPromises()
    button('艾米')!.click()
    await nextTick()
    expect(text()).toContain('该人物卡正在游戏中，无法存取仓库。')
    button('丢弃')!.click()
    await flushPromises()
    expect(useCharactersStore().characters[0]).toMatchObject({ inventory: ['手电筒'], in_session: true })
    expect(useUiStore().toasts.map((t) => t.message)).toContain('已从物品栏移除')
  })

  test('详情弹窗里确认框按 Esc 取消，不会把详情弹窗一起关掉', async () => {
    wrapper = mountDashboard()
    await flushPromises()
    button('艾米')!.click()
    await nextTick()
    button('遗忘')!.click()
    await nextTick()
    expect(text()).toContain('确定要遗忘法术「束缚术」吗？')
    // NOTE: 真实 Esc 从获得焦点的元素冒泡到 document，这里从确认框内的按钮派发
    document.body.querySelector('[role="alertdialog"] button')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect(document.body.querySelector('[role="alertdialog"]')).toBeNull()
    expect(document.body.querySelector('[role="dialog"]')).not.toBeNull()
  })

  test('仓库：取出需要目标人物卡；丢弃经确认；无可用人物卡时给出提示', async () => {
    routes['GET /api/warehouse'] = () => ({ warehouse: ['绷带', '地图'] })
    routes['POST /api/warehouse/withdraw'] = () => ({ warehouse: ['地图'], character_card: { ...alive, inventory: ['绷带', '手电筒', '绷带'] } })
    routes['DELETE /api/warehouse/items/%E5%9C%B0%E5%9B%BE'] = () => ({ warehouse: [] })
    wrapper = mountDashboard()
    await flushPromises()
    button('仓库')!.click()
    await flushPromises()
    expect(text()).toContain('账号仓库')
    expect(text()).toContain('地图')

    const select = document.body.querySelector('[role="dialog"] select') as HTMLSelectElement
    expect(select.value).toBe('1')
    calls = []
    ;[...document.body.querySelectorAll('[role="dialog"] button')].find((b) => b.textContent?.trim() === '取出')!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(calls[0]).toMatchObject({ path: '/api/warehouse/withdraw', body: { character_card_id: 1, item: '绷带' } })
    expect(text()).not.toContain('绷带 ')
    expect(useUiStore().toasts.map((t) => t.message)).toContain('已取出到人物卡')

    ;[...document.body.querySelectorAll('[role="dialog"] button')].find((b) => b.textContent?.trim() === '丢弃')!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    expect(text()).toContain('确定要丢弃仓库中的「地图」吗？此操作不可撤销。')
    await confirmDialog(true)
    expect(text()).toContain('仓库空空如也。')
  })

  test('仓库：所有人物卡都在游戏中时提示无法取出', async () => {
    routes['GET /api/characters'] = () => ({ items: [{ ...structuredClone(alive), in_session: true }], total_pages: 1 })
    routes['GET /api/warehouse'] = () => ({ warehouse: [] })
    wrapper = mountDashboard()
    await flushPromises()
    button('仓库')!.click()
    await flushPromises()
    expect(text()).toContain('所有人物卡都在游戏中，暂时无法取出物品。')
  })
})

describe('创建角色弹窗', () => {
  test('AI 生成：姓名为空时按钮禁用；成功后写入列表、刷新用户并提示', async () => {
    routes['GET /api/characters/skill-defaults'] = () => ({ skill_defaults: { 侦查: 25 }, skill_names: ['侦查', '克苏鲁神话'] })
    routes['POST /api/characters/generate'] = () => ({ ...alive, id: 3, name: '新人' })
    routes['GET /api/auth/me'] = () => ({ id: 9, username: 'u', role: 'user', coins: 900 })
    wrapper = mountDashboard()
    await flushPromises()
    button('+ 创建角色')!.click()
    await flushPromises()
    expect(button('AI 生成')!.disabled).toBe(true)

    // NOTE: 规则车卡面板（v-show 隐藏）在 DOM 里排在前面，AI 面板的输入框是最后一个同名元素
    const name = [...document.body.querySelectorAll('[role="dialog"] [aria-label="姓名"]')].at(-1) as HTMLInputElement
    name.value = '新人'
    name.dispatchEvent(new Event('input'))
    await nextTick()
    calls = []
    button('AI 生成')!.click()
    await flushPromises()
    expect(calls[0]).toMatchObject({ path: '/api/characters/generate', body: { name: '新人', race: '人类', gender: '', occupation: '', era: '', background: '' } })
    expect('age' in (calls[0]!.body as object)).toBe(false)
    expect(useCharactersStore().characters.map((c) => c.name)).toEqual(['艾米', '新人'])
    expect(useAuthStore().user!.coins).toBe(900)
    expect(useUiStore().toasts.map((t) => t.message)).toContain('AI 调查员生成成功！')
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  })

  test('规则车卡：年龄越界提示；掷骰进入第 2 步；预算为负时无法创建', async () => {
    routes['GET /api/characters/skill-defaults'] = () => ({ skill_defaults: { 母语: 0, 闪避: 0, 侦查: 25 }, skill_names: ['侦查', '母语', '闪避', '克苏鲁神话'] })
    routes['POST /api/characters/roll'] = () => ({ draft_id: 7, token: 't', stats: { str: 50, con: 50, siz: 50, dex: 60, app: 50, int: 10, pow: 50, edu: 20, hp: 10, max_hp: 10, mp: 10, max_mp: 10, san: 50, max_san: 99, luck: 50, mov: 8, build: 0, db: '0' }, skill_defaults: { 母语: 0, 闪避: 0, 侦查: 25 }, skill_budget: { occupation: 80, interest: 20, total: 100 }, raw_rolls: { age_log: ['年龄修正'] } })
    wrapper = mountDashboard()
    await flushPromises()
    button('+ 创建角色')!.click()
    await flushPromises()
    button('规则车卡')!.click()
    await nextTick()

    const age = document.body.querySelector('input[type="number"][min="15"]') as HTMLInputElement
    age.value = '10'
    age.dispatchEvent(new Event('input'))
    await nextTick()
    button('服务器掷骰')!.click()
    await flushPromises()
    expect(useUiStore().toasts.map((t) => t.message)).toContain('年龄必须在15-90之间')
    expect(calls.some((c) => c.path === '/api/characters/roll')).toBe(false)

    age.value = '30'
    age.dispatchEvent(new Event('input'))
    await nextTick()
    button('服务器掷骰')!.click()
    await flushPromises()
    expect(calls.find((c) => c.path === '/api/characters/roll')!.body).toEqual({ age: 30 })
    expect(useUiStore().toasts.map((t) => t.message)).toContain('属性已锁定，请继续填写调查员资料')
    expect(text()).toContain('已锁定属性')
    expect(text()).toContain('· 年龄修正')
    expect(button('下一步：分配技能')!.disabled).toBe(true)

    const name = document.body.querySelector('[role="dialog"] [aria-label="姓名"]') as HTMLInputElement
    name.value = '艾米'
    name.dispatchEvent(new Event('input'))
    const gender = [...document.body.querySelectorAll('[role="dialog"] select')].find((s) => (s as HTMLSelectElement).getAttribute('aria-label') === '性别') as HTMLSelectElement
    gender.value = '女'
    gender.dispatchEvent(new Event('change'))
    await nextTick()
    button('下一步：分配技能')!.click()
    await nextTick()
    expect(text()).toContain('技能点：职业 80 + 兴趣 20')
    expect(text()).toContain('剩余 100')
    const labels = [...document.body.querySelectorAll('label')].map((l) => l.textContent?.trim())
    expect(labels).toContain('侦查')
    expect(labels).not.toContain('克苏鲁神话')

    const spot = document.body.querySelector('#skill-input-0') as HTMLInputElement
    spot.value = '90'
    spot.dispatchEvent(new Event('input'))
    await nextTick()
    expect(text()).toContain('剩余 35')
    expect(button('确认创建并生成背景')!.disabled).toBe(false)
  })
})
