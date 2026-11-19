import { describe, expect, test } from 'vitest'
import type { Session } from '@/api/types'
import { buildReplayMarkdown } from './replayExport'
import type { RoomMessage } from './roomState'

const baseSession: Session = {
  id: 1,
  name: '雨夜灯塔',
  status: 'ended',
  created_by: 1,
  scenario: { id: 1, name: '孤岛迷雾' },
  players: [
    { user_id: 1, user: { username: 'alice' }, character_card: { name: '爱丽丝' } },
    { user_id: 2, user: { username: 'bob' }, character_card: { name: '鲍勃' } },
  ],
}

const sampleMessages: RoomMessage[] = [
  {
    id: 1,
    role: 'assistant',
    kind: 'event',
    content: '',
    meta: { events: [{ type: 'dice', character: '爱丽丝', skill: '侦查', roll: 12, target: 60, result: '极难成功' }] },
  },
  {
    id: 2,
    role: 'assistant',
    kind: 'writer',
    content: '海风拍打着灯塔厚重的铁门，门缝里渗出一丝潮湿的腥气。',
  },
  {
    id: 3,
    role: 'assistant',
    kind: 'kp',
    visibility: 'private',
    content: '你隐约听见门后有人低声念出你的名字。',
  },
  {
    id: 4,
    role: 'user',
    kind: 'player',
    sender_name: '爱丽丝',
    content: '我举起手电筒照向铁门锁孔。',
  },
]

describe('buildReplayMarkdown', () => {
  test('完整战报包含元数据、事件骰点、叙事正文、密语标记与玩家发言', () => {
    const md = buildReplayMarkdown(baseSession, sampleMessages)
    expect(md).toContain('# 雨夜灯塔')
    expect(md).toContain('模组**：孤岛迷雾')
    expect(md).toContain('爱丽丝（@alice）、鲍勃（@bob）')
    expect(md).toContain('爱丽丝 侦查 骰值 **12**')
    expect(md).toContain('海风拍打着灯塔厚重的铁门')
    expect(md).toContain('**【KP（密语）】**')
    expect(md).toContain('**【爱丽丝】**：我举起手电筒照向铁门锁孔。')
  })

  test('小说模式隐藏 event 骰点与结算事件，仅保留故事文本', () => {
    const md = buildReplayMarkdown(baseSession, sampleMessages, { includeEvents: false })
    expect(md).toContain('小说模式')
    expect(md).not.toContain('侦查 骰值')
    expect(md).toContain('海风拍打着灯塔厚重的铁门')
    expect(md).toContain('**【爱丽丝】**：我举起手电筒照向铁门锁孔。')
  })
})
