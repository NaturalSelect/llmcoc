import { describe, expect, test } from 'vitest'
import type { ClassifierLog } from '@/api/classifierTypes'
import {
  barPercent,
  CLASSIFIER_FILTERS,
  CLASSIFIER_LOG_LIMIT,
  decisionLabel,
  decisionTone,
  executionNote,
  fmt2,
  fmtClock,
  fmtDateTime,
  fmtElapsed,
  fmtPercent,
  fmtScore,
  isHit,
  kindLabel,
  mergeClassifierLogs,
  normalizeClassifierLog,
  normalizeClassifierLogs,
  optionLabel,
  probabilityRows,
  requestEntries,
  verdictText,
  visibleLogs,
} from './classifier'

function log(id: number, extra: Partial<ClassifierLog> = {}): ClassifierLog {
  return {
    id,
    kind: 'input_guard',
    created_at: '2026-11-28T10:20:30Z',
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
    request_id: 'req_abc',
    ...extra,
  }
}

const ids = (list: ClassifierLog[]) => list.map((l) => l.id)

describe('normalizeClassifierLog', () => {
  test('完整记录原样通过', () => {
    const raw = log(12, { kind: 'nsfw_route', decision: 'nsfw', floor: 5, role: 'director_nsfw', subject: '甲、乙' })
    expect(normalizeClassifierLog(raw)).toStrictEqual(raw)
  })

  test('id 不合法、kind 未知、不是对象的丢弃', () => {
    expect(normalizeClassifierLog(null)).toBeNull()
    expect(normalizeClassifierLog('x')).toBeNull()
    expect(normalizeClassifierLog([])).toBeNull()
    expect(normalizeClassifierLog({ ...log(1), id: 0 })).toBeNull()
    expect(normalizeClassifierLog({ ...log(1), id: -3 })).toBeNull()
    expect(normalizeClassifierLog({ ...log(1), id: 'abc' })).toBeNull()
    expect(normalizeClassifierLog({ ...log(1), id: undefined })).toBeNull()
    expect(normalizeClassifierLog({ ...log(1), kind: 'other' })).toBeNull()
    expect(normalizeClassifierLog({ ...log(1), kind: undefined })).toBeNull()
  })

  test('缺字段补默认值，不抛错；id 是数字串也接受', () => {
    expect(normalizeClassifierLog({ id: '7', kind: 'nsfw_route' })).toStrictEqual({
      id: 7,
      kind: 'nsfw_route',
      created_at: '',
      user_id: 0,
      subject: '',
      floor: 0,
      decision: '',
      status: 'ok',
      error: '',
      choice: '',
      score: 0,
      threshold: 0,
      probabilities: {},
      request: {},
      role: '',
      elapsed_ms: 0,
      request_id: '',
    })
  })

  test('status 只认 error，其余都按 ok', () => {
    expect(normalizeClassifierLog({ ...log(1), status: 'error' })?.status).toBe('error')
    expect(normalizeClassifierLog({ ...log(1), status: 'weird' })?.status).toBe('ok')
  })

  test('probabilities 里不是有限数字的项丢弃；request 的值转成文本，null 跳过', () => {
    const out = normalizeClassifierLog({
      ...log(1),
      probabilities: { a: 0.5, b: 'x', c: null, d: Number.NaN, e: 1 },
      request: { s: '多行\n文本', n: 42, o: { k: 1 }, z: null, u: undefined },
    })!
    expect(out.probabilities).toStrictEqual({ a: 0.5, e: 1 })
    expect(out.request).toStrictEqual({ s: '多行\n文本', n: '42', o: '{"k":1}' })
  })

  test('probabilities 和 request 不是对象时按空对象处理', () => {
    const out = normalizeClassifierLog({ ...log(1), probabilities: [1, 2], request: 'oops' })!
    expect(out.probabilities).toStrictEqual({})
    expect(out.request).toStrictEqual({})
  })

  test('normalizeClassifierLogs：null、非数组返回空数组，非法项跳过', () => {
    expect(normalizeClassifierLogs(null)).toStrictEqual([])
    expect(normalizeClassifierLogs({})).toStrictEqual([])
    expect(ids(normalizeClassifierLogs([log(1), null, { id: 2 }, log(3)]))).toStrictEqual([1, 3])
  })
})

describe('mergeClassifierLogs', () => {
  test('顺序到达直接追加', () => {
    const list: ClassifierLog[] = []
    mergeClassifierLogs(list, [log(1), log(2)])
    mergeClassifierLogs(list, [log(3)])
    expect(ids(list)).toStrictEqual([1, 2, 3])
  })

  test('按 id 去重：已有的不被覆盖，同一批里的重复也只留一条', () => {
    const list = [log(1, { subject: '原来的' }), log(2)]
    mergeClassifierLogs(list, [log(1, { subject: '重复的' }), log(2), log(3), log(3, { subject: '同批重复' })])
    expect(ids(list)).toStrictEqual([1, 2, 3])
    expect(list[0].subject).toBe('原来的')
    expect(list[2].subject).toBe('梅布尔')
  })

  test('乱序到达也保持 id 升序', () => {
    const list: ClassifierLog[] = []
    mergeClassifierLogs(list, [log(5)])
    mergeClassifierLogs(list, [log(2)])
    mergeClassifierLogs(list, [log(9), log(7), log(3), log(7)])
    expect(ids(list)).toStrictEqual([2, 3, 5, 7, 9])
  })

  test('历史（旧到新）与已有的实时记录重叠合并', () => {
    const list = [log(8), log(9)]
    mergeClassifierLogs(list, [log(6), log(7), log(8), log(9), log(10)])
    expect(ids(list)).toStrictEqual([6, 7, 8, 9, 10])
  })

  test('超过上限只保留 id 最大的那些', () => {
    const list: ClassifierLog[] = []
    mergeClassifierLogs(list, Array.from({ length: 12 }, (_, i) => log(i + 1)), 10)
    expect(ids(list)).toStrictEqual([3, 4, 5, 6, 7, 8, 9, 10, 11, 12])
  })

  test('比保留窗口还旧的记录并入后立刻被截掉', () => {
    const list = Array.from({ length: 10 }, (_, i) => log(i + 11))
    mergeClassifierLogs(list, [log(1)], 10)
    expect(ids(list)).toStrictEqual([11, 12, 13, 14, 15, 16, 17, 18, 19, 20])
  })

  test('默认上限是 500', () => {
    expect(CLASSIFIER_LOG_LIMIT).toBe(500)
    const list: ClassifierLog[] = []
    mergeClassifierLogs(list, Array.from({ length: 520 }, (_, i) => log(i + 1)))
    expect(list).toHaveLength(500)
    expect(list[0].id).toBe(21)
    expect(list[499].id).toBe(520)
  })
})

describe('visibleLogs', () => {
  const all = [log(1), log(2, { kind: 'nsfw_route' }), log(3), log(4, { kind: 'nsfw_route' })]

  test('全部：新的在上，不改原数组', () => {
    expect(ids(visibleLogs(all, 'all'))).toStrictEqual([4, 3, 2, 1])
    expect(ids(all)).toStrictEqual([1, 2, 3, 4])
  })

  test('按 kind 筛选', () => {
    expect(ids(visibleLogs(all, 'input_guard'))).toStrictEqual([3, 1])
    expect(ids(visibleLogs(all, 'nsfw_route'))).toStrictEqual([4, 2])
  })

  test('筛选项：全部 / 输入分类 / NSFW 路由', () => {
    expect(CLASSIFIER_FILTERS.map((f) => [f.key, f.label])).toStrictEqual([
      ['all', '全部'],
      ['input_guard', '输入分类'],
      ['nsfw_route', 'NSFW 路由'],
    ])
  })
})

describe('标签与色调', () => {
  test('选项中文名：八个已知选项，未知的原样显示', () => {
    expect(
      ['in_character', 'table_talk', 'authority_claim', 'spoiler_request', 'prompt_injection', 'sexual_explicit', 'sexual_suggestive', 'non_sexual'].map(optionLabel),
    ).toStrictEqual(['角色内行动', '桌边闲聊', '自称权威', '索要谜底', '提示词注入', '露骨性内容', '性暗示', '无性内容'])
    expect(optionLabel('brand_new_option')).toBe('brand_new_option')
  })

  test('kind 标签，未知的原样显示', () => {
    expect(kindLabel('input_guard')).toBe('输入分类')
    expect(kindLabel('nsfw_route')).toBe('NSFW 路由')
    expect(kindLabel('future')).toBe('future')
    expect(kindLabel('')).toBe('未知')
  })

  test('决定标签：五种取值；出错的放行带「（出错）」，failed 不重复加', () => {
    const d = (decision: string, status: 'ok' | 'error' = 'ok') => decisionLabel({ decision, status })
    expect(['blocked', 'passed', 'nsfw', 'clean', 'failed'].map((x) => d(x))).toStrictEqual(['已拒收', '放行', '涉及 NSFW', '不涉及', '失败'])
    expect(d('passed', 'error')).toBe('放行（出错）')
    expect(d('failed', 'error')).toBe('失败')
    expect(d('mystery')).toBe('mystery')
    expect(d('')).toBe('未知')
  })

  test('决定色调：拒收和涉及 NSFW 醒目，失败和出错警示，放行和不涉及平和', () => {
    const t = (decision: string, status: 'ok' | 'error' = 'ok') => decisionTone({ decision, status })
    expect(t('blocked')).toBe('danger')
    expect(t('nsfw')).toBe('seal')
    expect(t('failed')).toBe('warning')
    expect(t('passed')).toBe('moss')
    expect(t('clean')).toBe('moss')
    expect(t('mystery')).toBe('faint')
    // NOTE: 出错优先于 decision：输入分类出错时的放行不能显示成平和的绿色
    expect(t('passed', 'error')).toBe('warning')
    expect(t('clean', 'error')).toBe('warning')
  })

  test('本周期执行的角色：只有 nsfw_route 且有角色才有', () => {
    expect(executionNote({ kind: 'nsfw_route', role: 'director' })).toBe('本周期执行：Director')
    expect(executionNote({ kind: 'nsfw_route', role: 'director_nsfw' })).toBe('本周期执行：NSFW Director')
    expect(executionNote({ kind: 'nsfw_route', role: 'custom' })).toBe('本周期执行：custom')
    expect(executionNote({ kind: 'nsfw_route', role: '' })).toBe('')
    expect(executionNote({ kind: 'input_guard', role: 'director' })).toBe('')
  })
})

describe('分数、概率与格式化', () => {
  test('fmtScore：两位小数的「分数 / 阈值」；出错时不显示占位的 0 分', () => {
    expect(fmtScore({ status: 'ok', score: 0.55, threshold: 0.5 })).toBe('0.55 / 0.50')
    expect(fmtScore({ status: 'ok', score: 0.856, threshold: 0.8 })).toBe('0.86 / 0.80')
    expect(fmtScore({ status: 'ok', score: 1, threshold: 1 })).toBe('1.00 / 1.00')
    expect(fmtScore({ status: 'error', score: 0, threshold: 0.8 })).toBe('—')
    expect(fmt2(Number.NaN)).toBe('—')
  })

  test('命中：score >= threshold，等于也算；出错不算', () => {
    expect(isHit({ status: 'ok', score: 0.8, threshold: 0.8 })).toBe(true)
    expect(isHit({ status: 'ok', score: 0.79, threshold: 0.8 })).toBe(false)
    expect(isHit({ status: 'error', score: 0, threshold: 0 })).toBe(false)
    expect(verdictText({ status: 'ok', score: 0.9, threshold: 0.8 })).toBe('命中（分数 ≥ 阈值）')
    expect(verdictText({ status: 'ok', score: 0.1, threshold: 0.8 })).toBe('未命中（分数 < 阈值）')
    expect(verdictText({ status: 'error', score: 0, threshold: 0.8 })).toBe('没有得到有效分数')
  })

  test('概率：降序，同分按名字排，标出最高项', () => {
    const rows = probabilityRows({ in_character: 0.1, authority_claim: 0.55, spoiler_request: 0.25, table_talk: 0.1 }, 'authority_claim')
    expect(rows.map((r) => r.name)).toStrictEqual(['authority_claim', 'spoiler_request', 'in_character', 'table_talk'])
    expect(rows.map((r) => r.label)).toStrictEqual(['自称权威', '索要谜底', '角色内行动', '桌边闲聊'])
    expect(rows.map((r) => r.top)).toStrictEqual([true, false, false, false])
    expect(rows[0]).toMatchObject({ percent: 55, text: '55.0%', value: 0.55 })
  })

  test('概率：choice 缺失或不在表里时，取排第一的为最高项；未知选项原样显示', () => {
    const rows = probabilityRows({ zzz: 0.7, aaa: 0.3 }, 'not_there')
    expect(rows.map((r) => [r.label, r.top])).toStrictEqual([
      ['zzz', true],
      ['aaa', false],
    ])
    expect(probabilityRows({ a: 0.2, b: 0.8 }).map((r) => r.top)).toStrictEqual([true, false])
  })

  test('概率：空、null 返回空数组；非有限数跳过', () => {
    expect(probabilityRows({})).toStrictEqual([])
    expect(probabilityRows(null)).toStrictEqual([])
    expect(probabilityRows({ a: Number.NaN, b: 0.5 }).map((r) => r.name)).toStrictEqual(['b'])
  })

  test('横条宽度夹在 0 到 100 之间，保留一位小数', () => {
    expect(barPercent(0.5556)).toBe(55.6)
    expect(barPercent(-0.2)).toBe(0)
    expect(barPercent(1.7)).toBe(100)
    expect(barPercent(Number.NaN)).toBe(0)
    expect(fmtPercent(0.123)).toBe('12.3%')
    expect(fmtPercent(Number.NaN)).toBe('—')
  })

  test('request 保持键的顺序；null 和 undefined 给空列表', () => {
    expect(requestEntries({ player_input: 'a\nb', recent_table_log: 'c' })).toStrictEqual([
      { key: 'player_input', value: 'a\nb' },
      { key: 'recent_table_log', value: 'c' },
    ])
    expect(requestEntries(null)).toStrictEqual([])
    expect(requestEntries(undefined)).toStrictEqual([])
  })

  // NOTE: 不带时区的时间串按本地时间解析，所以这些断言与运行机器的时区无关
  test('时间：HH:MM:SS 与 YYYY-MM-DD HH:MM:SS，补零；空和非法返回空串', () => {
    expect(fmtClock('2026-11-28T09:05:03')).toBe('09:05:03')
    expect(fmtDateTime('2026-01-02T09:05:03')).toBe('2026-01-02 09:05:03')
    expect(fmtClock('')).toBe('')
    expect(fmtClock(null)).toBe('')
    expect(fmtClock('not a date')).toBe('')
    expect(fmtDateTime(undefined)).toBe('')
    expect(fmtClock('2026-11-28T10:20:30Z')).toMatch(/^\d{2}:\d{2}:\d{2}$/)
  })

  test('耗时：0ms 也显示；超过一秒用秒；非法给破折号', () => {
    expect(fmtElapsed(0)).toBe('0ms')
    expect(fmtElapsed(412)).toBe('412ms')
    expect(fmtElapsed(1500)).toBe('1.5s')
    expect(fmtElapsed(-1)).toBe('—')
    expect(fmtElapsed(Number.NaN)).toBe('—')
    expect(fmtElapsed(null)).toBe('—')
  })
})
