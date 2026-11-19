import { describe, expect, test } from 'vitest'
import {
  askAnchor,
  askAnswer,
  askCharCount,
  askLocked,
  askLockNote,
  askOptions,
  askTag,
  askView,
  cancelEdit,
  characterMadnessLabel,
  createAskDraft,
  draftAnswer,
  findBurnableFailedCheck,
  insertDialogueQuotes,
  isCharacterMad,
  quickCharacterItems,
  quickCharacterSkills,
  resetAskDraft,
  resetDraftOnFrame,
  startEdit,
  toggleOption,
} from './ask'
import * as RS from './roomState'
import type { Room, RoomMessage } from './roomState'

const SPOT_ME = { user_ids: [2], names: ['爱丽丝'], question: '怎么办？', kp_floor: false, asked_me: true, stop: '' }

function askingRoom(): Room {
  const room = RS.newRoom({ sessionId: 7, userId: 2 })
  RS.onHello(room, { epoch: 'E1', seq: 3 })
  RS.applySnapshot(room, {
    session: { status: 'playing' },
    messages: [],
    chatStatus: { spotlight: SPOT_ME, waiting: { pending: 1, total: 1 } },
  })
  return room
}

const ask = (id: number, options: unknown[]): RoomMessage => ({ id, kind: 'ask', status: 'done', meta: { options } })

describe('ask', () => {
  test('推荐行动取最近一条点名消息的选项，空白选项被丢弃，玩家行动之后不再推荐', () => {
    const room = askingRoom()
    expect(askOptions(room)).toStrictEqual([])

    room.messages.push(ask(200, ['查看地板', ' ', '敲门']))
    expect(askOptions(room)).toStrictEqual(['查看地板', '敲门'])

    room.messages.push({ id: 201, kind: 'player', status: 'done', user_id: 2 })
    expect(askOptions(room)).toStrictEqual([])

    room.messages.pop()
    room.messages.push({ id: 202, kind: 'ask', status: 'done', meta: {} })
    expect(askOptions(room)).toStrictEqual([])
  })

  test('没有被点名或周期运行中时没有选项也没有问题卡', () => {
    const room = askingRoom()
    room.messages.push(ask(200, ['敲门']))
    room.floor.running = true
    expect(askOptions(room)).toStrictEqual([])
    expect(askView(room, createAskDraft())).toBe('')
    room.floor.running = false
    room.spotlight.askedMe = false
    expect(askOptions(room)).toStrictEqual([])
    expect(askView(room, createAskDraft())).toBe('')
    expect(askView(null, createAskDraft())).toBe('')
  })

  test('问题卡挂在最近一条点名消息下面；玩家已发言、没被点名或没有点名消息时没有锚点', () => {
    const room = askingRoom()
    expect(askAnchor(room)).toBeNull()

    room.messages.push(ask(200, []), { id: 201, kind: 'writer', status: 'done' }, ask(202, []), { id: 203, kind: 'event', status: 'done' })
    expect(askAnchor(room)).toBe(202)

    room.messages.push({ id: 204, kind: 'player', status: 'done', user_id: 2 })
    expect(askAnchor(room)).toBeNull()
    room.messages.pop()

    room.floor.running = true
    expect(askAnchor(room)).toBeNull()
    room.floor.running = false
    room.spotlight.askedMe = false
    expect(askAnchor(room)).toBeNull()
    expect(askAnchor(null)).toBeNull()
  })

  test('勾选的推荐行动按选项顺序在前，自己写的在后，不在选项里的勾选被忽略', () => {
    const room = askingRoom()
    room.messages.push(ask(200, ['查看地板', '敲门']))
    const draft = createAskDraft()

    toggleOption(room, draft, '敲门')
    toggleOption(room, draft, '查看地板')
    toggleOption(room, draft, '查看地板')
    toggleOption(room, draft, '查看地板')
    expect(draft.pick).toStrictEqual(['敲门', '查看地板'])
    expect(draftAnswer(room, draft)).toBe('查看地板\n敲门')

    draft.input = '我推门'
    expect(draftAnswer(room, draft)).toBe('查看地板\n敲门\n我推门')
    expect(askCharCount(draftAnswer(room, draft))).toBe([...'查看地板\n敲门\n我推门'].length)

    draft.pick = ['敲门', '不存在的选项']
    expect(draftAnswer(room, draft)).toBe('敲门\n我推门')
    expect(askAnswer(['a', 'b'], [], '  ')).toBe('')
  })

  test('已提交后可以修改、取消修改、重新提交', () => {
    const room = askingRoom()
    const draft = createAskDraft()
    room.me.submitted = true
    room.me.draft = '我推门'
    expect(askView(room, draft)).toBe('done')

    startEdit(room, draft)
    expect(draft.editing).toBe(true)
    expect(askView(room, draft)).toBe('card')
    expect(draft.input).toBe('我推门')

    cancelEdit(draft)
    expect(askView(room, draft)).toBe('done')
    expect(draft.input).toBe('')
    expect(room.me.draft).toBe('我推门')

    startEdit(room, draft)
    draft.input = '我踹门'
    resetAskDraft(draft)
    expect(draft).toStrictEqual({ pick: [], editing: false, input: '' })
  })

  test('没有被点名时不能进入修改', () => {
    const room = askingRoom()
    room.spotlight.askedMe = false
    const draft = createAskDraft()
    startEdit(room, draft)
    expect(draft.editing).toBe(false)
  })

  test('断线或同步快照时锁定：勾选不变，提示文案区分原因', () => {
    const room = askingRoom()
    room.messages.push(ask(200, ['敲门']))
    const draft = createAskDraft()
    expect(askLocked(room)).toBe(false)
    expect(askLockNote(room)).toBe('')

    toggleOption(room, draft, '敲门')
    RS.markDisconnected(room)
    expect(askLocked(room)).toBe(true)
    expect(askLockNote(room)).toMatch(/连接中断/)
    toggleOption(room, draft, '敲门')
    expect(draft.pick).toStrictEqual(['敲门'])

    room.conn = 'snapshot'
    expect(askLockNote(room)).toMatch(/同步/)
  })

  test('标签：多人点名列出所有名字，单人是向你提问', () => {
    const room = askingRoom()
    expect(askTag(room)).toBe('KP 向你提问')
    room.spotlight.names = ['爱丽丝', '鲍勃']
    expect(askTag(room)).toBe('KP 点名：爱丽丝、鲍勃，请各自回应')
    expect(askTag(null)).toBe('KP 向你提问')
  })

  test('新周期开始：清掉上一个问题残留的勾选、修改状态和草稿，其他帧不动', () => {
    const draft = createAskDraft()
    draft.pick = ['x']
    draft.editing = true
    draft.input = '残留'

    resetDraftOnFrame(draft, { type: 'kp_status' })
    resetDraftOnFrame(draft, { type: 'message_new' })
    expect(draft).toStrictEqual({ pick: ['x'], editing: true, input: '残留' })

    resetDraftOnFrame(draft, { type: 'floor_start' })
    expect(draft).toStrictEqual({ pick: [], editing: false, input: '' })
  })

  test('插入「」：无选中时光标落进括号内，有选中时包住选中内容', () => {
    expect(insertDialogueQuotes('abc', 1, 1)).toStrictEqual({ value: 'a「」bc', caret: 2 })
    expect(insertDialogueQuotes('abcd', 1, 3)).toStrictEqual({ value: 'a「bc」d', caret: 5 })
    expect(insertDialogueQuotes('', 0, 0)).toStrictEqual({ value: '「」', caret: 1 })
  })

  test('燃烧幸运：识别本周期失败的普通技能检定，按 need 算消耗，排除大失败/理智/幸运/已烧幸运', () => {
    const room = askingRoom()
    room.messages.push(
      {
        id: 210,
        kind: 'event',
        status: 'done',
        meta: {
          events: [{ type: 'dice', character: '爱丽丝', skill: '侦查', roll: 38, target: 60, need: 30, level: '困难', result: '失败' }],
        },
      },
      ask(211, []),
    )
    expect(findBurnableFailedCheck(room, '爱丽丝', 50)).toStrictEqual({
      skill: '侦查',
      roll: 38,
      need: 30,
      target: 60,
      level: '困难',
      cost: 8,
      affordable: true,
      pushable: true,
    })
    expect(findBurnableFailedCheck(room, '爱丽丝', 5)?.affordable).toBe(false)

    // 已经消耗幸运后不再提示
    room.messages.push({
      id: 212,
      kind: 'event',
      status: 'done',
      meta: { events: [{ type: 'stat', character: '爱丽丝', field: 'luck', from: 50, to: 42 }] },
    })
    expect(findBurnableFailedCheck(room, '爱丽丝', 42)).toBeNull()

    // 理智与大失败不可燃烧幸运
    const room2 = askingRoom()
    room2.messages.push({
      id: 220,
      kind: 'event',
      status: 'done',
      meta: {
        events: [
          { type: 'dice', character: '爱丽丝', skill: '理智', roll: 80, target: 50, need: 50, result: '失败' },
          { type: 'dice', character: '爱丽丝', skill: '开锁', roll: 99, target: 40, need: 40, result: '大失败' },
        ],
      },
    })
    expect(findBurnableFailedCheck(room2, '爱丽丝', 50)).toBeNull()
  })

  test('技能与物品速查：按数值降序过滤技能并解析背包物品', () => {
    const card = {
      name: '爱丽丝',
      skills: { 侦查: 65, 图书馆使用: 70, 潜水: 10 },
      inventory: [{ name: '手电筒', desc: '电量满', count: 1, equipped: true }, { name: '绷带', count: 3 }],
    }
    expect(quickCharacterSkills(card)).toStrictEqual([
      { name: '图书馆使用', value: 70 },
      { name: '侦查', value: 65 },
    ])
    expect(quickCharacterItems(card)).toStrictEqual([
      { name: '手电筒', desc: '电量满', count: 1, equipped: true },
      { name: '绷带', desc: '', count: 3, equipped: false },
    ])
  })

  test('潜伏疯狂期判定仅覆盖 temporary 与 indefinite', () => {
    expect(isCharacterMad({ name: '爱丽丝', madness_state: 'temporary' })).toBe(true)
    expect(characterMadnessLabel({ name: '爱丽丝', madness_state: 'temporary' })).toBe('临时性疯狂')
    expect(isCharacterMad({ name: '爱丽丝', madness_state: 'indefinite' })).toBe(true)
    expect(characterMadnessLabel({ name: '爱丽丝', madness_state: 'indefinite' })).toBe('不定性疯狂')
    expect(isCharacterMad({ name: '爱丽丝', madness_state: 'permanent' })).toBe(false)
    expect(isCharacterMad({ name: '爱丽丝', madness_state: 'none' })).toBe(false)
    expect(isCharacterMad(null)).toBe(false)
  })
})

