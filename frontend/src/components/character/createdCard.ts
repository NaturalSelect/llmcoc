import type { CharacterCardFull } from '@/api/characterTypes'
import { useAuthStore } from '@/stores/auth'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'

// NOTE: 创建请求可能耗时很久（AI 生成），用户中途关掉弹窗后组件已卸载，emit 会被 Vue 丢弃；
// 所以创建成功后的列表更新、金币/卡槽刷新和提示都直接走 store，不依赖组件事件
export async function registerCreatedCard(card: CharacterCardFull, message: string): Promise<void> {
  const chars = useCharactersStore()
  const auth = useAuthStore()
  const ui = useUiStore()
  chars.characters = [...chars.characters, card]
  await auth.loadMe().catch(() => {})
  ui.toast(message)
}
