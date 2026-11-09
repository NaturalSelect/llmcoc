// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import App from '@/App.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import PaginationBar from '@/components/base/PaginationBar.vue'
import StatBar from '@/components/base/StatBar.vue'
import TabBar from '@/components/base/TabBar.vue'
import { createAppRouter } from '@/router'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  document.body.innerHTML = ''
  vi.unstubAllGlobals()
})

describe('BaseModal', () => {
  test('打开时渲染到 body，Esc 和点击遮罩触发关闭，点击内容不关闭', async () => {
    const wrapper = mount(BaseModal, { props: { open: true, title: '标题' }, slots: { default: '<p id="inner">内容</p>' }, attachTo: document.body })
    await nextTick()
    expect(document.body.querySelector('[role="dialog"]')).not.toBeNull()
    expect(document.body.querySelector('#inner')?.textContent).toBe('内容')

    document.body.querySelector('#inner')!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(wrapper.emitted('close')).toBeUndefined()

    ;(document.body.querySelector('[role="dialog"]')!.parentElement as HTMLElement).click()
    expect(wrapper.emitted('close')).toHaveLength(1)

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(wrapper.emitted('close')).toHaveLength(2)
    wrapper.unmount()
  })

  test('关闭后不再响应 Esc；焦点回到打开前的元素', async () => {
    const trigger = document.createElement('button')
    document.body.appendChild(trigger)
    trigger.focus()
    const wrapper = mount(BaseModal, { props: { open: true }, attachTo: document.body })
    await nextTick()
    expect(document.activeElement).toBe(document.body.querySelector('[role="dialog"]'))

    await wrapper.setProps({ open: false })
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(trigger)

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(wrapper.emitted('close')).toBeUndefined()
    wrapper.unmount()
  })

  test('卸载时移除键盘监听', async () => {
    const wrapper = mount(BaseModal, { props: { open: true }, attachTo: document.body })
    await nextTick()
    wrapper.unmount()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(wrapper.emitted('close')).toBeUndefined()
  })

  test('内容区单独滚动，footer 插槽固定在滚动区之外；没有插槽时不渲染 footer', async () => {
    const withFooter = mount(BaseModal, {
      props: { open: true, title: '标题' },
      slots: { default: '<p id="body">内容</p>', footer: '<button id="act">创建</button>' },
      attachTo: document.body,
    })
    await nextTick()
    const body = document.body.querySelector('#body')!.parentElement as HTMLElement
    expect(body.className).toContain('overflow-y-auto')
    const footer = document.body.querySelector('footer')
    expect(footer?.querySelector('#act')).not.toBeNull()
    expect(body.contains(footer)).toBe(false)
    withFooter.unmount()

    const plain = mount(BaseModal, { props: { open: true }, slots: { default: '<p>内容</p>' }, attachTo: document.body })
    await nextTick()
    expect(document.body.querySelector('footer')).toBeNull()
    plain.unmount()
  })
})

describe('StatBar', () => {
  test('百分比夹在 0 到 100，max 非正为 0', () => {
    const width = (value: number, max: number) =>
      (mount(StatBar, { props: { label: 'HP', value, max } }).find('[role="progressbar"] > div').element as HTMLElement).style.width
    expect(width(5, 10)).toBe('50%')
    expect(width(20, 10)).toBe('100%')
    expect(width(-3, 10)).toBe('0%')
    expect(width(5, 0)).toBe('0%')
  })

  test('flash 为真时带闪烁样式', () => {
    expect(mount(StatBar, { props: { label: 'HP', value: 1, max: 2, flash: true } }).classes()).toContain('stat-flash')
    expect(mount(StatBar, { props: { label: 'HP', value: 1, max: 2 } }).classes()).not.toContain('stat-flash')
  })
})

describe('PaginationBar', () => {
  test('只有一页时不显示；翻页被夹在合法范围内', async () => {
    expect(mount(PaginationBar, { props: { page: 1, totalPages: 1 } }).html()).toBe('<!--v-if-->')

    const wrapper = mount(PaginationBar, { props: { page: 1, totalPages: 3, total: 55, 'onUpdate:page': (p: number) => wrapper.setProps({ page: p }) } })
    const [prev, next] = wrapper.findAll('button')
    expect(prev.attributes('disabled')).toBeDefined()
    await next.trigger('click')
    await next.trigger('click')
    expect(wrapper.text()).toContain('3 / 3')
    expect(wrapper.text()).toContain('共 55 条')
    expect(wrapper.findAll('button')[1].attributes('disabled')).toBeDefined()
  })
})

describe('TabBar', () => {
  test('点击切换选中项', async () => {
    const Host = defineComponent({
      setup() {
        const tab = ref('a')
        return () =>
          h('div', [
            h(TabBar, { tabs: [{ key: 'a', label: '甲' }, { key: 'b', label: '乙' }], modelValue: tab.value, 'onUpdate:modelValue': (v: string) => (tab.value = v) }),
            h('span', { id: 'cur' }, tab.value),
          ])
      },
    })
    const wrapper = mount(Host)
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    expect(wrapper.find('#cur').text()).toBe('b')
    expect(wrapper.findAll('[role="tab"]')[1].attributes('aria-selected')).toBe('true')
  })
})

describe('BaseInput', () => {
  test('.number 修饰：数字字符串转数字，空串保持空串', async () => {
    const Host = defineComponent({
      setup() {
        const v = ref<string | number | null | undefined>('')
        const onUpdate = (x: unknown) => {
          v.value = x as string | number
        }
        return () =>
          h('div', [
            h(BaseInput, { modelValue: v.value, modelModifiers: { number: true }, 'onUpdate:modelValue': onUpdate }),
            h('i', { id: 'out' }, JSON.stringify(v.value)),
          ])
      },
    })
    const wrapper = mount(Host)
    const input = wrapper.find('input')
    await input.setValue('42')
    expect(wrapper.find('#out').text()).toBe('42')
    await input.setValue('')
    expect(wrapper.find('#out').text()).toBe('""')
  })
})

describe('App 外壳', () => {
  async function mountApp(path: string, user: object | null) {
    if (user) {
      localStorage.setItem('coc_token', 't')
      vi.stubGlobal('fetch', vi.fn(async (url: string) => ({ ok: true, status: 200, json: async () => (url === '/api/auth/me' ? user : { items: [] }) }) as Response))
    }
    setActivePinia(createPinia())
    const router = createAppRouter(createMemoryHistory())
    await router.push(path)
    await router.isReady()
    const wrapper = mount(App, { global: { plugins: [router] }, attachTo: document.body })
    await nextTick()
    return { wrapper, router }
  }

  test('登录页不显示顶栏', async () => {
    const { wrapper } = await mountApp('/login', null)
    expect(wrapper.find('nav').exists()).toBe(false)
  })

  test('普通用户顶栏没有管理入口，并显示金币与卡槽', async () => {
    const { wrapper } = await mountApp('/', { id: 1, username: 'alice', role: 'user', coins: 321, card_slots: 5 })
    const text = wrapper.find('nav').text()
    expect(text).toContain('首页')
    expect(text).toContain('跑团大厅')
    expect(text).toContain('商城')
    expect(text).not.toContain('管理')
    expect(text).toContain('321')
    expect(text).toContain('alice')
    expect(text).toMatch(/0\s*\/\s*5/)
  })

  test('顶栏的人物卡数与当前页面无关：直达游戏页也会拉取人物卡', async () => {
    const calls: string[] = []
    localStorage.setItem('coc_token', 't')
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        calls.push(url)
        const body = url === '/api/auth/me' ? { id: 1, username: 'alice', role: 'user', card_slots: 3 } : url.startsWith('/api/characters') ? { items: [{ id: 1, name: '艾米' }], total_pages: 1 } : {}
        return { ok: true, status: 200, json: async () => body } as Response
      }),
    )
    vi.stubGlobal('WebSocket', class { close() {} })
    setActivePinia(createPinia())
    const router = createAppRouter(createMemoryHistory())
    await router.push('/shop')
    await router.isReady()
    const wrapper = mount(App, { global: { plugins: [router] }, attachTo: document.body })
    await vi.waitFor(() => expect(wrapper.find('nav').text()).toMatch(/1\s*\/\s*3/))
    expect(calls.some((c) => c.startsWith('/api/characters'))).toBe(true)
    wrapper.unmount()
  })

  test('管理员顶栏有管理入口；当前页链接高亮', async () => {
    const { wrapper } = await mountApp('/shop', { id: 1, username: 'root', role: 'admin' })
    expect(wrapper.find('nav').text()).toContain('管理')
    const active = wrapper.findAll('nav a').filter((a) => a.attributes('data-active') === 'true')
    expect(active.map((a) => a.text())).toContain('商城')
  })

  test('退出：清空登录态并回到登录页', async () => {
    const { wrapper, router } = await mountApp('/', { id: 1, username: 'alice', role: 'user' })
    const logout = wrapper.findAll('nav button').find((b) => b.text() === '退出')!
    await logout.trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('login'))
    expect(useAuthStore().isLoggedIn).toBe(false)
    expect(localStorage.getItem('coc_token')).toBeNull()
  })

  test('全局 toast 与确认框由外壳承载', async () => {
    const { wrapper } = await mountApp('/', { id: 1, username: 'alice', role: 'user' })
    const ui = useUiStore()
    ui.toast('保存成功')
    const pending = ui.confirm('确定删除？', { danger: true, confirmText: '删除' })
    await nextTick()
    expect(document.body.textContent).toContain('保存成功')
    expect(document.body.textContent).toContain('确定删除？')
    const confirmBtn = [...document.body.querySelectorAll('button')].find((b) => b.textContent?.trim() === '删除')!
    expect(confirmBtn.getAttribute('data-variant')).toBe('danger')
    confirmBtn.click()
    expect(await pending).toBe(true)
    wrapper.unmount()
  })
})
