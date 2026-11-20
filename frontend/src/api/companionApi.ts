import { api } from './client'
import type {
  AddCompanionPayload,
  AddCompanionResponse,
  AdminCompanionCard,
  CompanionCard,
  CompanionCardsResponse,
  CompanionMessageResponse,
} from './companionTypes'

const roomPath = (sessionId: number) => `/api/sessions/${sessionId}/companion`

// NOTE: 房间内三个接口只有房主或管理员能调用，且只有大厅阶段可写；管理员后台三个接口管卡池
export const companionApi = {
  // NOTE: Go 的 nil 切片会序列化成 null，这里统一成数组；enabled 缺省按未启用处理
  cards: async (sessionId: number): Promise<CompanionCardsResponse> => {
    const res = await api<Partial<CompanionCardsResponse> | null>('GET', `${roomPath(sessionId)}-cards`)
    return { enabled: res?.enabled === true, cards: Array.isArray(res?.cards) ? (res.cards as CompanionCard[]) : [] }
  },
  add: (sessionId: number, payload: AddCompanionPayload) => api<AddCompanionResponse>('POST', roomPath(sessionId), payload),
  remove: (sessionId: number) => api<CompanionMessageResponse>('DELETE', roomPath(sessionId)),

  adminList: async (): Promise<AdminCompanionCard[]> => {
    const res = await api<AdminCompanionCard[] | null>('GET', '/api/admin/companion-cards')
    return Array.isArray(res) ? res : []
  },
  adminAdd: (cardId: number) => api<CompanionMessageResponse>('POST', '/api/admin/companion-cards', { card_id: cardId }),
  adminReturn: (cardId: number) => api<CompanionMessageResponse>('POST', `/api/admin/companion-cards/${cardId}/return`),
}
