// @vitest-environment jsdom
// NOTE: 挂载真实的 MessageItem（玩家消息分支）：管理员在气泡下方看到分类器 chip、点开展开详情；非管理员的 DOM 里没有任何分类器内容。
// 详情里有链接，所以需要路由；管理员身份来自 auth store
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import { afterEach, describe, expect, test, vi } from 'vitest'
import { reactive } from 'vue'
import type { ClassifierLog } from '@/api/classifierTypes'
import { createAppRouter } from '@/router'
import { newRoom } from '@/game/roomState'
import type { Room, RoomMessage } from '@/game/roomState'
import { useAuthStore } from '@/stores/auth'
import MessageItem from './MessageItem.vue'

function log(extra: Partial<ClassifierLog> = {}): ClassifierLog {
  return {
    id: 1,
    kind: 'input_guard',
    created_at: '2026-11-28T10:20:30',
    user_id: 2,
    subject: '爱丽丝',
    floor: 2,
    decision: 'passed',
    status: 'ok',
    error: '',
    choice: 'in_character',
    score: 0.12,
    threshold: 0.8,
    probabilities: { in_character: 0.88, authority_claim: 0.12 },
    request: { player_input: '我推门进去' },
    role: '',
    elapsed_ms: 412,
    request_id: '',
    ...extra,
  }
}

const ROUTE: Partial<ClassifierLog> = {
  id: 2,
  kind: 'nsfw_route',
  user_id: 0,
  decision: 'nsfw',
  role: 'director_nsfw',
  score: 0.72,
  threshold: 0.5,
  choice: 'sexual_suggestive',
  probabilities: { sexual_suggestive: 0.6, sexual_explicit: 0.12, non_sexual: 0.28 },
  request: { player_actions: '爱丽丝：我推门进去' },
}

function playerMsg(extra: Partial<RoomMessage> = {}): RoomMessage {
  return { id: 4, role: 'user', kind: 'player', status: 'done', floor: 2, user_id: 2, username: 'alice', content: '我推门进去', created_at: '2026-11-28T10:01:04', ...extra }
}

const mounted: Array<{ unmount(): void }> = []

afterEach(() => {
  for (const w of mounted.splice(0)) w.unmount()
  localStorage.clear()
  vi.unstubAllGlobals()
})

interface MountOptions {
  admin?: boolean
  logs?: readonly ClassifierLog[]
  msg?: RoomMessage
}

async function mountItem({ admin = true, logs, msg = playerMsg() }: MountOptions = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  localStorage.setItem('coc_token', 'tok')
  const auth = useAuthStore()
  auth.user = { id: 2, username: 'alice', role: admin ? 'admin' : 'user' }
  auth.meLoaded = true
  const router = createAppRouter(createMemoryHistory())
  await router.push('/')
  await router.isReady()
  const room = reactive(newRoom({ sessionId: 7, userId: 2, isHost: false })) as Room
  const wrapper = mount(MessageItem, { props: { msg, room, classifierLogs: logs }, global: { plugins: [pinia, router] } })
  mounted.push(wrapper)
  return wrapper
}

type Wrapper = Awaited<ReturnType<typeof mountItem>>
const chips = (w: Wrapper) => w.findAll('[data-testid="classifier-chip"]')

describe('MessageItem 内嵌分类器（管理员）', () => {
  test('玩家气泡下方（同一个 article 里）一排 chip，写明种类、判定和分数/阈值', async () => {
    const w = await mountItem({ logs: [log(), log(ROUTE)] })
    const group = w.find('article > [data-testid="classifier-chips"]')
    expect(group.exists()).toBe(true)
    expect(group.attributes('role')).toBe('group')
    // NOTE: chip 组在气泡之后
    const article = w.find('article').element
    expect(article.firstElementChild?.textContent).toContain('我推门进去')
    expect(article.lastElementChild).toBe(group.element)

    expect(group.findAll('[data-testid="chip-kind"]').map((n) => n.text())).toStrictEqual(['输入分类', 'NSFW 路由'])
    expect(group.findAll('[data-testid="chip-decision"]').map((n) => n.text())).toStrictEqual(['放行', '走 NSFW Director'])
    expect(group.findAll('[data-testid="chip-score"]').map((n) => n.text())).toStrictEqual(['0.12 / 0.80', '0.72 / 0.50'])
  })

  test('每个 chip 是按钮：点击展开详情（概率、判定、请求内容、去系统设置的链接），再点收起', async () => {
    const w = await mountItem({ logs: [log(), log(ROUTE)] })
    const [first, second] = chips(w)
    expect(first.element.tagName).toBe('BUTTON')
    expect(first.attributes('type')).toBe('button')
    expect(first.attributes('aria-expanded')).toBe('false')
    expect(w.find('[data-testid="classifier-detail"]').exists()).toBe(false)

    await first.trigger('click')
    expect(first.attributes('aria-expanded')).toBe('true')
    expect(second.attributes('aria-expanded')).toBe('false')
    const detail = w.find('[data-testid="classifier-detail"]')
    expect(detail.attributes('id')).toBe(first.attributes('aria-controls'))
    expect(detail.findAll('[data-testid="prob-row"]')).toHaveLength(2)
    expect(detail.find('[data-testid="detail-verdict"]').text()).toContain('阈值 0.80')
    expect(detail.findAll('[data-testid="request-value"]').map((n) => n.text())).toStrictEqual(['我推门进去'])
    const link = detail.find('[data-testid="detail-settings-link"] a')
    expect(link.attributes('href')).toBe('/admin/settings')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.text()).toContain('系统设置')

    await second.trigger('click')
    expect(w.findAll('[data-testid="classifier-detail"]')).toHaveLength(2)

    await first.trigger('click')
    expect(first.attributes('aria-expanded')).toBe('false')
    expect(w.findAll('[data-testid="classifier-detail"]')).toHaveLength(1)
  })

  test('自己的消息 chip 行靠右（和气泡一致），别人的消息靠左；宽度上限与气泡相同，详情在 chip 行之外占整行', async () => {
    const mine = await mountItem({ logs: [log()] })
    const row = mine.find('[data-testid="classifier-chips"] > div')
    expect(row.classes()).toEqual(expect.arrayContaining(['ml-auto', 'w-fit', 'max-w-full', 'sm:max-w-[85%]']))
    expect(mine.find('article > div').classes()).toContain('ml-auto')

    await chips(mine)[0].trigger('click')
    const detail = mine.find('[data-testid="classifier-detail"]')
    expect(row.element.contains(detail.element)).toBe(false)
    expect(detail.element.parentElement).toBe(mine.find('[data-testid="classifier-chips"]').element)
    expect(detail.classes().some((c) => c.startsWith('max-w-') || c === 'w-fit' || c === 'ml-auto')).toBe(false)

    const theirs = await mountItem({ logs: [log({ user_id: 3 })], msg: playerMsg({ user_id: 3, username: 'bob' }) })
    expect(theirs.find('[data-testid="classifier-chips"] > div').classes()).not.toContain('ml-auto')
    expect(theirs.find('article > div').classes()).not.toContain('ml-auto')
  })

  test('记录被同 id 改写后 chip 随之更新，已展开的详情保持展开', async () => {
    const w = await mountItem({ logs: [log()] })
    await chips(w)[0].trigger('click')
    await w.setProps({ classifierLogs: [log({ decision: 'blocked', score: 0.9, subject: '改写后' })] })
    expect(w.find('[data-testid="chip-decision"]').text()).toBe('已拒收')
    expect(w.find('[data-testid="chip-score"]').text()).toBe('0.90 / 0.80')
    expect(chips(w)[0].attributes('aria-expanded')).toBe('true')
    expect(w.find('[data-testid="detail-subject"]').text()).toBe('改写后')
  })

  test('请求内容里的标签只作为文本显示，不会生成元素', async () => {
    const evil = '<img src=x onerror=alert(1)><script>boom()</script>'
    const w = await mountItem({ logs: [log({ request: { player_input: evil } })] })
    await chips(w)[0].trigger('click')
    expect(w.find('[data-testid="request-value"]').text()).toBe(evil)
    expect(w.find('article img').exists()).toBe(false)
    expect(w.find('article script').exists()).toBe(false)
  })

  test('没有记录（缺省或空数组）时没有 chip 组；只有玩家消息才显示，旁白、点名、系统消息不显示', async () => {
    expect((await mountItem()).find('[data-testid="classifier-chips"]').exists()).toBe(false)
    expect((await mountItem({ logs: [] })).find('[data-testid="classifier-chips"]').exists()).toBe(false)
    for (const kind of ['kp', 'ask', 'writer', 'system', 'event']) {
      const w = await mountItem({ logs: [log()], msg: { id: 9, role: 'assistant', kind, status: 'done', floor: 2, content: '内容' } })
      expect(w.find('[data-testid="classifier-chips"]').exists(), kind).toBe(false)
    }
  })
})

describe('MessageItem 内嵌分类器（非管理员）', () => {
  test('即使被传入了记录，DOM 里也没有任何分类器内容，也不发请求', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    const w = await mountItem({ admin: false, logs: [log({ request: { player_input: '机密输入' } }), log(ROUTE)] })
    expect(w.text()).toContain('我推门进去')
    expect(w.find('[data-testid="classifier-chips"]').exists()).toBe(false)
    expect(w.find('[data-testid="classifier-chip"]').exists()).toBe(false)
    expect(w.html()).not.toContain('classifier')
    expect(w.text()).not.toContain('输入分类')
    expect(w.text()).not.toContain('NSFW 路由')
    expect(w.text()).not.toContain('机密输入')
    expect(fetchSpy).not.toHaveBeenCalled()
  })
})
