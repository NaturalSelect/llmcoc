import { api } from '@/api/client'
import type {
  Announcement,
  CharacterCardFull,
  FinalizePayload,
  GeneratePayload,
  RecentSession,
  RegenField,
  RegenResponse,
  ReviveResponse,
  RollDraft,
  SkillDefaultsResponse,
  WarehouseResponse,
  WarehouseTransferResponse,
} from '@/api/characterTypes'
import type { Page } from '@/api/pagination'

const enc = encodeURIComponent
const cardPath = (id: number) => `/api/characters/${id}`

// NOTE: 路径里的名称（物品/法术/社会关系/资产）可能含中文、斜杠和空格，必须 encodeURIComponent，与旧前端一致
export const characterApi = {
  skillDefaults: () => api<SkillDefaultsResponse>('GET', '/api/characters/skill-defaults'),
  roll: (age: number) => api<RollDraft>('POST', '/api/characters/roll', { age }),
  finalize: (payload: FinalizePayload) => api<CharacterCardFull>('POST', '/api/characters/finalize', payload),
  generate: (payload: GeneratePayload) => api<CharacterCardFull>('POST', '/api/characters/generate', payload),

  get: (id: number) => api<CharacterCardFull>('GET', cardPath(id)),
  remove: (id: number) => api<{ message?: string }>('DELETE', cardPath(id)),
  removeDead: (id: number) => api<{ message?: string }>('DELETE', `${cardPath(id)}/dead`),
  revive: (id: number) => api<ReviveResponse>('POST', `${cardPath(id)}/revive`),

  regenerate: (id: number, field: RegenField, guidance: string) =>
    api<RegenResponse>('POST', `${cardPath(id)}/regenerate-${field}`, { guidance }),

  removeInventoryItem: (id: number, item: string) =>
    api<CharacterCardFull>('DELETE', `${cardPath(id)}/inventory/${enc(item)}`),
  removeSocialRelation: (id: number, name: string) =>
    api<CharacterCardFull>('DELETE', `${cardPath(id)}/social-relations/${enc(name)}`),
  removeAsset: (id: number, name: string) => api<CharacterCardFull>('DELETE', `${cardPath(id)}/assets/${enc(name)}`),
  forgetSpell: (id: number, spell: string) => api<CharacterCardFull>('DELETE', `${cardPath(id)}/spells/${enc(spell)}`),

  warehouse: () => api<WarehouseResponse>('GET', '/api/warehouse'),
  deposit: (cardId: number, item: string) =>
    api<WarehouseTransferResponse>('POST', '/api/warehouse/deposit', { character_card_id: cardId, item }),
  withdraw: (cardId: number, item: string) =>
    api<WarehouseTransferResponse>('POST', '/api/warehouse/withdraw', { character_card_id: cardId, item }),
  discard: (item: string) => api<WarehouseResponse>('DELETE', `/api/warehouse/items/${enc(item)}`),

  announcements: () => api<Announcement[] | null>('GET', '/api/announcements'),
  recentSessions: (limit: number) => api<Page<RecentSession>>('GET', `/api/sessions?page=1&page_size=${limit}`),
}
