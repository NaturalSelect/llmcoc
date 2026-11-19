// @vitest-environment jsdom
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { nextTick } from 'vue'
import ConfirmHost from '@/components/base/ConfirmHost.vue'
import AuctionView from '@/views/AuctionView.vue'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'

interface Call {
  method: string
  path: string
  body: any
}

class Fail {
  constructor(
    public status: number,
    public body: unknown,
  ) {}
}

let calls: Call[]
let routes: Record<string, (call: Call) => unknown>

const mkListing = (over: Record<string, unknown>) => ({
  seller_id: 2,
  seller_name: '老王',
  item_desc: '',
  item_type: 'misc',
  count: 1,
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
  ...over,
})
const bandage = mkListing({ id: 1, item_name: '绷带', item_desc: '染血', count: 3, price: 50 })
const myMap = mkListing({ id: 2, seller_id: 9, seller_name: 'me', item_name: '地图', price: 30 })
const pistol = mkListing({ id: 3, item_name: '手枪', item_type: 'weapon', price: 500 })

function respond(body: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => body } as Response
}

function install() {
  vi.stubGlobal('fetch', async (path: string, init: RequestInit = {}) => {
    const call: Call = { method: init.method as string, path, body: init.body ? JSON.parse(init.body as string) : undefined }
    calls.push(call)
    const handler = routes[`${call.method} ${path}`] ?? routes[`${call.method} ${path.split('?')[0]}`]
    if (!handler) return respond({ error: `unrouted ${call.method} ${path}` }, 500)
    const out = handler(call)
    return out instanceof Fail ? respond(out.body, out.status) : respond(out)
  })
}

function mountView() {
  return mount({ components: { AuctionView, ConfirmHost }, template: '<AuctionView /><ConfirmHost />' }, { attachTo: document.body })
}

async function confirmDialog(accept: boolean) {
  const buttons = [...document.body.querySelectorAll('[role="alertdialog"] button')] as HTMLButtonElement[]
  buttons.find((b) => (accept ? b.textContent?.trim() !== '取消' : b.textContent?.trim() === '取消'))!.click()
  await flushPromises()
}

const text = () => document.body.textContent ?? ''
const buttons = (label: string) => [...document.body.querySelectorAll('button')].filter((b) => b.textContent?.trim() === label) as HTMLButtonElement[]
const button = (label: string) => buttons(label)[0]
const toasts = () => useUiStore().toasts.map((t) => t.message)

function type(el: Element, value: string) {
  ;(el as HTMLInputElement).value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

async function openListingDialog() {
  button('上架物品')!.click()
  await flushPromises()
}

let wrapper: VueWrapper

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  document.body.innerHTML = ''
  calls = []
  routes = {
    'GET /api/auction/listings': () => ({ items: [bandage, myMap, pistol], page: 1, total: 3, total_pages: 1 }),
    'GET /api/auction/mine': () => ({ items: [], page: 1, total: 0, total_pages: 1 }),
    'GET /api/warehouse': () => ({ warehouse: [{ name: '绷带', desc: '染血', count: 3, type: 'misc' }] }),
  }
  install()
  useAuthStore().user = { id: 9, username: 'me', role: 'user', coins: 120 }
})

afterEach(() => {
  wrapper?.unmount()
  vi.unstubAllGlobals()
})

describe('AuctionView', () => {
  test('渲染在售物品：别人的挂单可购买，自己的挂单显示下架，买不起的购买按钮置灰', async () => {
    wrapper = mountView()
    await flushPromises()
    expect(text()).toContain('当前金币：120')
    expect(text()).toContain('绷带')
    expect(text()).toContain('x3')
    expect(text()).toContain('染血')
    expect(text()).toContain('卖家：老王')
    expect(text()).toContain('我的挂单')

    const buy = buttons('购买')
    expect(buy).toHaveLength(2)
    expect(buy[0]!.disabled).toBe(false)
    expect(buy[1]!.disabled).toBe(true)
    expect(buy[1]!.title).toBe('金币不足')
    expect(buttons('下架')).toHaveLength(1)
  })

  test('没有在售物品时显示空状态', async () => {
    routes['GET /api/auction/listings'] = () => ({ items: [], page: 1, total: 0, total_pages: 1 })
    wrapper = mountView()
    await flushPromises()
    expect(text()).toContain('暂无在售物品')
  })

  test('购买：取消不发请求；确认后扣款、刷新金币并重新拉取列表', async () => {
    routes['POST /api/auction/listings/1/buy'] = () => ({ coins: 70, listing: bandage, warehouse: [] })
    wrapper = mountView()
    await flushPromises()
    calls = []

    button('购买')!.click()
    await nextTick()
    expect(text()).toContain('确认花费 50 金币购买「绷带(染血, x3)」？')
    await confirmDialog(false)
    expect(calls).toHaveLength(0)

    button('购买')!.click()
    await nextTick()
    await confirmDialog(true)
    expect(calls.map((c) => `${c.method} ${c.path.split('?')[0]}`)).toEqual(['POST /api/auction/listings/1/buy', 'GET /api/auction/listings'])
    expect(useAuthStore().user?.coins).toBe(70)
    expect(toasts()).toContain('购买成功，物品已放入仓库')
  })

  test('购买被服务端拒绝（已被别人买走）：提示原因并刷新列表', async () => {
    routes['POST /api/auction/listings/1/buy'] = () => new Fail(409, { error: '该挂单已成交或已下架' })
    wrapper = mountView()
    await flushPromises()
    calls = []

    button('购买')!.click()
    await nextTick()
    await confirmDialog(true)
    expect(toasts()).toContain('该挂单已成交或已下架')
    expect(useAuthStore().user?.coins).toBe(120)
    expect(calls.at(-1)).toMatchObject({ method: 'GET', path: expect.stringContaining('/api/auction/listings?') })
  })

  test('搜索和筛选：搜索词与类型、排序进入查询串', async () => {
    wrapper = mountView()
    await flushPromises()
    expect(calls.at(-1)!.path).toBe('/api/auction/listings?page=1&page_size=12&sort=newest')

    type(document.body.querySelector('input[aria-label="搜索物品名"]')!, ' 绷 ')
    document.body.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushPromises()
    expect(calls.at(-1)!.path).toBe('/api/auction/listings?page=1&page_size=12&sort=newest&q=%E7%BB%B7')

    const typeSelect = document.body.querySelector('select[aria-label="物品类型"]') as HTMLSelectElement
    typeSelect.value = 'weapon'
    typeSelect.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    expect(calls.at(-1)!.path).toContain('type=weapon')

    const sortSelect = document.body.querySelector('select[aria-label="排序"]') as HTMLSelectElement
    sortSelect.value = 'price_asc'
    sortSelect.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    expect(calls.at(-1)!.path).toContain('sort=price_asc')
  })

  test('上架：读取仓库、确认后提交，成功后切到我的挂单', async () => {
    routes['POST /api/auction/listings'] = () => ({ listing: mkListing({ id: 9, seller_id: 9, item_name: '绷带', price: 80 }), warehouse: [] })
    routes['GET /api/auction/mine'] = () => ({
      items: [mkListing({ id: 9, seller_id: 9, item_name: '绷带', item_desc: '染血', count: 2, price: 80 })],
      page: 1,
      total: 1,
      total_pages: 1,
    })
    wrapper = mountView()
    await flushPromises()
    await openListingDialog()
    expect(text()).toContain('仓库里有 3 件')

    const inputs = document.body.querySelectorAll('[role="dialog"] input')
    type(inputs[0]!, '2')
    type(inputs[1]!, '80')
    calls = []
    button('上架')!.click()
    await nextTick()
    expect(text()).toContain('确认以 80 金币上架「绷带(染血, x2)」？')
    await confirmDialog(true)

    expect(calls[0]).toMatchObject({ method: 'POST', path: '/api/auction/listings', body: { item: { name: '绷带', desc: '染血' }, count: 2, price: 80 } })
    expect(calls.some((c) => c.method === 'GET' && c.path.startsWith('/api/auction/mine'))).toBe(true)
    expect(toasts()).toContain('已上架')
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    expect(text()).toContain('在售')
    expect(text()).toContain('绷带(染血, x2)')
  })

  test('上架：数量超过库存或价格缺失时不发请求', async () => {
    wrapper = mountView()
    await flushPromises()
    await openListingDialog()
    const inputs = document.body.querySelectorAll('[role="dialog"] input')
    calls = []

    type(inputs[0]!, '5')
    type(inputs[1]!, '80')
    button('上架')!.click()
    await flushPromises()
    expect(toasts()).toContain('仓库里只有 3 件')

    type(inputs[0]!, '1')
    type(inputs[1]!, '')
    button('上架')!.click()
    await flushPromises()
    expect(toasts()).toContain('价格必须是正整数')

    expect(calls).toHaveLength(0)
    expect(document.body.querySelector('[role="alertdialog"]')).toBeNull()
  })

  test('上架：仓库为空时给出提示并禁用提交', async () => {
    routes['GET /api/warehouse'] = () => ({ warehouse: [] })
    wrapper = mountView()
    await flushPromises()
    await openListingDialog()
    expect(text()).toContain('仓库里没有物品')
    expect(button('上架')!.disabled).toBe(true)
  })

  test('我的挂单：显示各状态，只有在售的可以下架，下架后刷新', async () => {
    routes['GET /api/auction/mine'] = () => ({
      items: [
        mkListing({ id: 2, seller_id: 9, item_name: '地图', price: 30, status: 'active' }),
        mkListing({ id: 4, seller_id: 9, item_name: '手电筒', price: 20, status: 'sold', closed_at: '2026-01-02T00:00:00Z' }),
        mkListing({ id: 5, seller_id: 9, item_name: '绳索', price: 10, status: 'cancelled', closed_at: '2026-01-03T00:00:00Z' }),
      ],
      page: 1,
      total: 3,
      total_pages: 1,
    })
    routes['DELETE /api/auction/listings/2'] = () => ({ message: '已下架，物品已退回仓库', listing: myMap, warehouse: [] })
    wrapper = mountView()
    await flushPromises()

    button('我的挂单')!.click()
    await flushPromises()
    expect(text()).toContain('已售出')
    expect(text()).toContain('已下架')
    expect(buttons('下架')).toHaveLength(1)

    calls = []
    button('下架')!.click()
    await nextTick()
    expect(text()).toContain('确认下架「地图」？物品会退回仓库。')
    await confirmDialog(true)
    expect(calls.map((c) => `${c.method} ${c.path.split('?')[0]}`)).toEqual(['DELETE /api/auction/listings/2', 'GET /api/auction/mine'])
    expect(toasts()).toContain('已下架，物品已退回仓库')
  })

  test('浏览页下架自己的挂单后重新拉取浏览列表', async () => {
    routes['DELETE /api/auction/listings/2'] = () => ({ listing: myMap, warehouse: [] })
    wrapper = mountView()
    await flushPromises()
    calls = []
    button('下架')!.click()
    await nextTick()
    await confirmDialog(true)
    expect(calls.map((c) => `${c.method} ${c.path.split('?')[0]}`)).toEqual(['DELETE /api/auction/listings/2', 'GET /api/auction/listings'])
  })

  test('当前页被清空时退回上一页', async () => {
    let served = 0
    routes['GET /api/auction/listings'] = (call) => {
      served++
      return call.path.includes('page=2') ? { items: [], page: 2, total: 12, total_pages: 1 } : { items: [bandage], page: 1, total: 12, total_pages: 2 }
    }
    wrapper = mountView()
    await flushPromises()
    ;(document.body.querySelector('[aria-label="下一页"]') as HTMLButtonElement).click()
    await flushPromises()
    expect(calls.map((c) => c.path).filter((p) => p.includes('/listings?')).slice(-2)).toEqual([
      '/api/auction/listings?page=2&page_size=12&sort=newest',
      '/api/auction/listings?page=1&page_size=12&sort=newest',
    ])
    expect(served).toBe(3)
    expect(text()).toContain('绷带')
  })
})
