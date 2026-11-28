// @vitest-environment jsdom
// NOTE: 挂载真实的 ClassifierDetail（含 RouterLink）：概率行、分数与阈值、判定文案、执行角色、错误提示、事实列表、请求原文、跳转系统设置的链接
import { mount } from '@vue/test-utils'
import type { DOMWrapper, VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import { afterEach, describe, expect, test } from 'vitest'
import type { ClassifierLog } from '@/api/classifierTypes'
import { createAppRouter } from '@/router'
import { useAuthStore } from '@/stores/auth'
import ClassifierDetail from './ClassifierDetail.vue'

// NOTE: 时间串不带时区，按本地时间解析，所以「2026-11-28 10:20:30」与运行机器的时区无关
function log(extra: Partial<ClassifierLog> = {}): ClassifierLog {
  return {
    id: 1,
    kind: 'input_guard',
    created_at: '2026-11-28T10:20:30',
    user_id: 3,
    subject: '梅布尔',
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

const BLOCKED: Partial<ClassifierLog> = {
  decision: 'blocked',
  choice: 'authority_claim',
  score: 0.85,
  threshold: 0.8,
  probabilities: { in_character: 0.1, table_talk: 0.05, authority_claim: 0.55, spoiler_request: 0.3, brand_new: 0 },
}

const NSFW: Partial<ClassifierLog> = {
  kind: 'nsfw_route',
  decision: 'nsfw',
  floor: 5,
  user_id: 0,
  role: 'director_nsfw',
  score: 0.72,
  threshold: 0.5,
  choice: 'sexual_suggestive',
  probabilities: { sexual_suggestive: 0.5, sexual_explicit: 0.22, non_sexual: 0.28 },
}

const ERRORED: Partial<ClassifierLog> = {
  kind: 'nsfw_route',
  decision: 'failed',
  status: 'error',
  error: 'HTTP 400（按安全过滤命中处理）',
  role: 'director',
  floor: 3,
  user_id: 0,
  score: 0,
  threshold: 0.5,
  choice: '',
  probabilities: {},
}

const flat = (s: string) => s.replace(/\s+/g, ' ').trim()

const mounted: Array<{ unmount(): void }> = []

afterEach(() => {
  for (const w of mounted.splice(0)) w.unmount()
  localStorage.clear()
})

// NOTE: 路由守卫要读 auth store（需要已激活的 pinia），所以先把管理员登录态摆好再导航；组件本身不依赖 pinia
async function makeRouter() {
  const pinia = createPinia()
  setActivePinia(pinia)
  localStorage.setItem('coc_token', 'tok')
  const auth = useAuthStore()
  auth.user = { id: 2, username: 'alice', role: 'admin' }
  auth.meLoaded = true
  const router = createAppRouter(createMemoryHistory())
  await router.push('/')
  await router.isReady()
  return router
}

async function mountDetail(extra: Partial<ClassifierLog> = {}) {
  const router = await makeRouter()
  const wrapper = mount(ClassifierDetail, { props: { log: log(extra) }, global: { plugins: [router] } })
  mounted.push(wrapper)
  return { wrapper: wrapper as VueWrapper, router }
}

const probRows = (w: VueWrapper) => w.findAll('[data-testid="prob-row"]')
const rowLabel = (r: DOMWrapper<Element>) => r.find('span').text()
// NOTE: dl 里按 dt 文案取对应 dd 的文本
const dtLabels = (w: VueWrapper) => w.findAll('dt').map((d) => d.text())
function fact(w: VueWrapper, label: string): string | undefined {
  const dt = w.findAll('dt').find((d) => d.text() === label)
  return dt?.element.nextElementSibling?.textContent ?? undefined
}

describe('ClassifierDetail 各选项概率', () => {
  test('按概率降序，最高项用 data-top 和「最高」标出，百分比、横条宽度和进度条语义正确', async () => {
    const { wrapper } = await mountDetail(BLOCKED)
    const rows = probRows(wrapper)
    expect(rows.map(rowLabel)).toStrictEqual(['自称权威', '索要谜底', '角色内行动', '桌边闲聊', 'brand_new'])
    expect(rows.map((r) => r.attributes('data-top'))).toStrictEqual(['true', 'false', 'false', 'false', 'false'])

    expect(rows[0]!.text()).toContain('55.0%')
    expect(rows[0]!.text()).toContain('最高')
    expect(rows[1]!.text()).toContain('30.0%')
    expect(rows[1]!.text()).not.toContain('最高')
    expect(rows[4]!.text()).toContain('0.0%')

    const bar = (r: DOMWrapper<Element>) => r.find('[role="progressbar"]')
    expect(bar(rows[0]!).attributes('aria-valuenow')).toBe('55')
    expect(bar(rows[0]!).attributes('aria-label')).toBe('自称权威')
    expect((bar(rows[0]!).find('div').element as HTMLElement).style.width).toBe('55%')
    expect((bar(rows[4]!).find('div').element as HTMLElement).style.width).toBe('0%')
    expect(bar(rows[0]!).find('div').classes()).toContain('bg-accent')
    expect(bar(rows[1]!).find('div').classes()).not.toContain('bg-accent')
  })

  test('选项名带中文标签，原始键名放在 title 里；未知选项原样显示', async () => {
    const { wrapper } = await mountDetail(BLOCKED)
    const rows = probRows(wrapper)
    expect(rows[0]!.find('span').attributes('title')).toBe('authority_claim')
    expect(rows[4]!.find('span').attributes('title')).toBe('brand_new')
    expect(rowLabel(rows[4]!)).toBe('brand_new')
  })

  test('choice 在概率表里就以 choice 为最高项，不在时取排第一的', async () => {
    const withChoice = await mountDetail({ choice: 'table_talk', probabilities: { in_character: 0.6, table_talk: 0.4 } })
    expect(probRows(withChoice.wrapper).map((r) => r.attributes('data-top'))).toStrictEqual(['false', 'true'])

    const noChoice = await mountDetail({ choice: 'gone', probabilities: { in_character: 0.6, table_talk: 0.4 } })
    expect(probRows(noChoice.wrapper).map((r) => r.attributes('data-top'))).toStrictEqual(['true', 'false'])
  })

  test('没有概率数据时给一句说明，不渲染任何行', async () => {
    const { wrapper } = await mountDetail({ probabilities: {}, choice: '' })
    expect(probRows(wrapper)).toHaveLength(0)
    expect(wrapper.find('section[aria-label="各选项概率"]').text()).toContain('没有概率数据')
  })
})

describe('ClassifierDetail 分数与阈值', () => {
  test('阈值标记按阈值定位（0.8 → left: 80%），分数条宽度按分数，命中时用强调色', async () => {
    const { wrapper } = await mountDetail(BLOCKED)
    const mark = wrapper.find('[data-testid="threshold-mark"]')
    expect((mark.element as HTMLElement).style.left).toBe('80%')
    expect(mark.attributes('aria-hidden')).toBe('true')

    const fill = wrapper.find('section[aria-label="分数与阈值"] .overflow-hidden > div')
    expect((fill.element as HTMLElement).style.width).toBe('85%')
    expect(fill.classes()).toContain('bg-accent')
  })

  test('阈值标记随阈值移动：0.5 → 50%，1 → 100%', async () => {
    const half = await mountDetail({ threshold: 0.5, score: 0.2 })
    expect((half.wrapper.find('[data-testid="threshold-mark"]').element as HTMLElement).style.left).toBe('50%')
    const full = await mountDetail({ threshold: 1, score: 0.2 })
    expect((full.wrapper.find('[data-testid="threshold-mark"]').element as HTMLElement).style.left).toBe('100%')
  })

  test('命中：分数 0.85 · 阈值 0.80 · 命中（分数 ≥ 阈值）', async () => {
    const { wrapper } = await mountDetail(BLOCKED)
    expect(flat(wrapper.find('[data-testid="detail-verdict"]').text())).toBe('分数 0.85 · 阈值 0.80 · 命中（分数 ≥ 阈值）')
  })

  test('未命中：分数条不用强调色，文案写未命中', async () => {
    const { wrapper } = await mountDetail({ score: 0.2, threshold: 0.5 })
    expect(flat(wrapper.find('[data-testid="detail-verdict"]').text())).toBe('分数 0.20 · 阈值 0.50 · 未命中（分数 < 阈值）')
    const fill = wrapper.find('section[aria-label="分数与阈值"] .overflow-hidden > div')
    expect((fill.element as HTMLElement).style.width).toBe('20%')
    expect(fill.classes()).not.toContain('bg-accent')
  })

  test('分数恰好等于阈值算命中', async () => {
    const { wrapper } = await mountDetail({ score: 0.5, threshold: 0.5 })
    expect(flat(wrapper.find('[data-testid="detail-verdict"]').text())).toBe('分数 0.50 · 阈值 0.50 · 命中（分数 ≥ 阈值）')
  })

  test('出错记录没有有效分数：不画分数条和阈值标记，只写阈值和「没有得到有效分数」', async () => {
    const { wrapper } = await mountDetail(ERRORED)
    expect(wrapper.find('[data-testid="threshold-mark"]').exists()).toBe(false)
    expect(wrapper.find('section[aria-label="分数与阈值"] .overflow-hidden').exists()).toBe(false)
    const verdict = flat(wrapper.find('[data-testid="detail-verdict"]').text())
    expect(verdict).toBe('阈值 0.50 · 没有得到有效分数')
    expect(verdict).not.toContain('分数 0.00')
  })
})

describe('ClassifierDetail 本周期执行的 Director', () => {
  test('nsfw_route 记录写明本周期执行的角色', async () => {
    const nsfw = await mountDetail(NSFW)
    expect(nsfw.wrapper.find('[data-testid="detail-role"]').text()).toBe('本周期执行：NSFW Director')
    const clean = await mountDetail({ ...NSFW, decision: 'clean', role: 'director' })
    expect(clean.wrapper.find('[data-testid="detail-role"]').text()).toBe('本周期执行：Director')
  })

  test('未知角色原样显示；路由记录没有角色、或输入分类记录时不显示这一行', async () => {
    const unknown = await mountDetail({ ...NSFW, role: 'director_x' })
    expect(unknown.wrapper.find('[data-testid="detail-role"]').text()).toBe('本周期执行：director_x')
    const noRole = await mountDetail({ ...NSFW, role: '' })
    expect(noRole.wrapper.find('[data-testid="detail-role"]').exists()).toBe(false)
    const input = await mountDetail({ role: 'director_nsfw' })
    expect(input.wrapper.find('[data-testid="detail-role"]').exists()).toBe(false)
  })
})

describe('ClassifierDetail 错误提示', () => {
  test('status 为 error 时显示错误原因，且是 role=note 的提示块', async () => {
    const { wrapper } = await mountDetail(ERRORED)
    const note = wrapper.find('[data-testid="detail-error"]')
    expect(note.text()).toBe('错误原因：HTTP 400（按安全过滤命中处理）')
    expect(note.attributes('role')).toBe('note')
  })

  test('出错却没给原因时写「未提供原因」；status 为 ok 但带 error 也照样显示', async () => {
    const empty = await mountDetail({ ...ERRORED, error: '' })
    expect(empty.wrapper.find('[data-testid="detail-error"]').text()).toBe('错误原因：未提供原因')
    const okWithError = await mountDetail({ status: 'ok', error: '超时' })
    expect(okWithError.wrapper.find('[data-testid="detail-error"]').text()).toBe('错误原因：超时')
  })

  test('正常记录没有错误提示', async () => {
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-testid="detail-error"]').exists()).toBe(false)
  })

  test('出错记录里概率为空对象：不渲染概率行', async () => {
    const { wrapper } = await mountDetail(ERRORED)
    expect(probRows(wrapper)).toHaveLength(0)
    expect(wrapper.text()).toContain('没有概率数据')
  })
})

describe('ClassifierDetail 事实列表', () => {
  test('输入分类记录：调查员、最高项（中文标签）、时间、用户 ID、耗时；周期和请求 ID 为空时不显示', async () => {
    const { wrapper } = await mountDetail()
    expect(dtLabels(wrapper)).toStrictEqual(['调查员', '最高项', '时间', '用户 ID', '耗时'])
    expect(wrapper.find('[data-testid="detail-subject"]').text()).toBe('梅布尔')
    expect(fact(wrapper, '最高项')).toBe('角色内行动')
    expect(fact(wrapper, '时间')).toBe('2026-11-28 10:20:30')
    expect(fact(wrapper, '用户 ID')).toBe('3')
    expect(fact(wrapper, '耗时')).toBe('412ms')
    expect(wrapper.find('[data-testid="detail-floor"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="detail-request-id"]').exists()).toBe(false)
  })

  test('周期大于 0 和有请求 ID 时才显示这两行', async () => {
    const { wrapper } = await mountDetail({ floor: 6, request_id: 'req_abc' })
    expect(dtLabels(wrapper)).toStrictEqual(['调查员', '最高项', '时间', '周期', '用户 ID', '耗时', '请求 ID'])
    expect(wrapper.find('[data-testid="detail-floor"]').text()).toBe('6')
    expect(wrapper.find('[data-testid="detail-request-id"]').text()).toBe('req_abc')
  })

  test('路由记录没有用户、调查员和最高项时只剩时间与耗时；周期照常显示', async () => {
    const { wrapper } = await mountDetail({ ...NSFW, subject: '', choice: '', floor: 0 })
    expect(dtLabels(wrapper)).toStrictEqual(['时间', '耗时'])
    const withFloor = await mountDetail({ ...NSFW, subject: '甲、乙' })
    expect(dtLabels(withFloor.wrapper)).toStrictEqual(['调查员', '最高项', '时间', '周期', '耗时'])
    expect(withFloor.wrapper.find('[data-testid="detail-subject"]').text()).toBe('甲、乙')
    expect(withFloor.wrapper.find('[data-testid="detail-floor"]').text()).toBe('5')
  })

  test('耗时：0 显示 0ms，超过一秒换算成秒；时间解析不了显示破折号', async () => {
    const zero = await mountDetail({ elapsed_ms: 0 })
    expect(fact(zero.wrapper, '耗时')).toBe('0ms')
    const slow = await mountDetail({ elapsed_ms: 1500 })
    expect(fact(slow.wrapper, '耗时')).toBe('1.5s')
    const noTime = await mountDetail({ created_at: '' })
    expect(fact(noTime.wrapper, '时间')).toBe('—')
  })
})

describe('ClassifierDetail 发给分类器的请求', () => {
  test('每个键值一块：键名在上，值放进等宽、保留换行的 pre', async () => {
    const { wrapper } = await mountDetail({
      request: { player_input: '第一行\n第二行\n\n  缩进的第四行', player_character: '梅布尔', recent_table_log: '', kp_question: '你要怎么做？' },
    })
    const entries = wrapper.findAll('[data-testid="request-entry"]')
    expect(entries.map((e) => e.find('p').text())).toStrictEqual(['player_input', 'player_character', 'recent_table_log', 'kp_question'])
    const values = entries.map((e) => e.find('pre'))
    expect(values[0]!.element.textContent).toBe('第一行\n第二行\n\n  缩进的第四行')
    expect(values[1]!.text()).toBe('梅布尔')
    expect(values[3]!.text()).toBe('你要怎么做？')
    expect(values[0]!.classes()).toEqual(expect.arrayContaining(['font-mono', 'whitespace-pre-wrap']))
  })

  test('空值显示「（空）」', async () => {
    const { wrapper } = await mountDetail({ request: { recent_table_log: '' } })
    expect(wrapper.find('[data-testid="request-value"]').text()).toBe('（空）')
  })

  test('没有请求内容时不渲染这一节', async () => {
    const { wrapper } = await mountDetail({ request: {} })
    expect(wrapper.find('section[aria-label="发给分类器的请求"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="request-entry"]')).toHaveLength(0)
  })

  test('请求里的 HTML 是纯文本：不产生 img、script、b 元素，pre 的文本等于原串', async () => {
    const evilInput = '<img src=x onerror=alert(1)><b>加粗</b>\n第二行'
    const evilQuestion = '<script>alert(1)</script>'
    const { wrapper } = await mountDetail({ request: { player_input: evilInput, kp_question: evilQuestion } })
    const values = wrapper.findAll('pre')
    expect(values.map((v) => v.element.textContent)).toStrictEqual([evilInput, evilQuestion])
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.find('b').exists()).toBe(false)
    expect(wrapper.element.querySelectorAll('[onerror]')).toHaveLength(0)
  })

  test('其余来自玩家和 KP 的字段（调查员名、错误原因、请求 ID、选项名）同样只当文本', async () => {
    const { wrapper } = await mountDetail({
      subject: '<img src=x onerror=alert(1)>',
      status: 'error',
      error: '<script>alert(2)</script>',
      request_id: '<b>id</b>',
      choice: '<i>top</i>',
      probabilities: { '<i>top</i>': 0.9 },
    })
    expect(wrapper.find('[data-testid="detail-subject"]').element.textContent).toBe('<img src=x onerror=alert(1)>')
    expect(wrapper.find('[data-testid="detail-error"]').text()).toBe('错误原因：<script>alert(2)</script>')
    expect(wrapper.find('[data-testid="detail-request-id"]').element.textContent).toBe('<b>id</b>')
    expect(rowLabel(probRows(wrapper)[0]!)).toBe('<i>top</i>')
    for (const tag of ['img', 'script', 'b', 'i']) expect(wrapper.find(tag).exists()).toBe(false)
  })
})

describe('ClassifierDetail 跳转系统设置的链接', () => {
  test('链接指向 /admin/settings，在新标签页打开并带 noopener，文案是「管理 → 系统设置」', async () => {
    const { wrapper } = await mountDetail()
    const box = wrapper.find('[data-testid="detail-settings-link"]')
    expect(box.text()).toContain('调整阈值')
    const link = box.find('a')
    expect(link.attributes('href')).toBe('/admin/settings')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('rel')).toContain('noopener')
    expect(link.text()).toContain('系统设置')
    expect(link.text()).toBe('管理 → 系统设置')
  })

  test('出错记录、没有请求内容的记录里链接也在', async () => {
    const errored = await mountDetail(ERRORED)
    expect(errored.wrapper.find('[data-testid="detail-settings-link"] a').attributes('href')).toBe('/admin/settings')
    const bare = await mountDetail({ request: {}, probabilities: {} })
    expect(bare.wrapper.find('[data-testid="detail-settings-link"] a').attributes('href')).toBe('/admin/settings')
  })

  test('点击不会在当前标签页里导航：游戏页留在原地，由浏览器开新标签', async () => {
    const { wrapper, router } = await mountDetail()
    const link = wrapper.find('[data-testid="detail-settings-link"] a').element
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 })
    link.dispatchEvent(ev)
    await Promise.resolve()
    // NOTE: RouterLink 遇到 target=_blank 会放手让浏览器处理：不 preventDefault，也不触发路由跳转
    expect(ev.defaultPrevented).toBe(false)
    expect(router.currentRoute.value.path).toBe('/')
  })
})
