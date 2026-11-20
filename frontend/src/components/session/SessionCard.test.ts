// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import type { LobbySession } from '@/api/sessionTypes'
import SessionCard from './SessionCard.vue'

const session = (players: LobbySession['players']): LobbySession => ({
  id: 1,
  name: '雾港旧宅',
  status: 'lobby',
  max_players: 4,
  created_by: 1,
  scenario: null,
  creator: { id: 1, username: 'host' },
  players,
})

describe('SessionCard 玩家标签', () => {
  test('AI 陪玩的标签带 AI 徽标，真人和没有 role 的旧数据不带', () => {
    const w = mount(SessionCard, {
      props: {
        session: session([
          { user_id: 1, user: { id: 1, username: 'alice', role: 'user' }, character_card: { id: 5, name: '爱丽丝' } },
          { user_id: 2, user: { id: 2, username: 'bob' }, character_card: { id: 6, name: '鲍勃' } },
          { user_id: 9, user: { id: 9, username: 'AI陪玩', role: 'bot' }, character_card: { id: 31, name: '陪玩医生' } },
        ]),
      },
    })
    const tags = w.findAll('ul[aria-label="玩家"] li')
    expect(tags).toHaveLength(3)
    expect(tags.map((t) => t.find('[title="AI 陪玩"]').exists())).toStrictEqual([false, false, true])
    expect(tags[2]!.text()).toContain('陪玩医生')
    expect(tags[2]!.text()).toContain('AI')
  })

  test('players 为 null（刚创建的房间）时不渲染玩家列表', () => {
    const w = mount(SessionCard, { props: { session: session(null) } })
    expect(w.find('ul[aria-label="玩家"]').exists()).toBe(false)
  })
})
