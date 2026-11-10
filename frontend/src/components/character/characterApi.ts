import { api } from '@/api/client'
import type {
  Announcement,
  CharacterCardFull,
  FinalizePayload,
  GeneratePayload,
  InventoryItem,
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

type ItemRef = Pick<InventoryItem, 'name' | 'desc'>

// NOTE: 物品由 name + desc 共同标识；count 缺省或 0 表示整堆，所以不带 count 参数
function itemQuery(it: ItemRef, count?: number): string {
  return `${enc(it.name)}?desc=${enc(it.desc ?? '')}${count ? `&count=${count}` : ''}`
}

const itemBody = (it: ItemRef) => ({ name: it.name, desc: it.desc ?? '' })

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

  removeInventoryItem: (id: number, it: ItemRef, count?: number) =>
    api<CharacterCardFull>('DELETE', `${cardPath(id)}/inventory/${itemQuery(it, count)}`),
  removeSocialRelation: (id: number, name: string) =>
    api<CharacterCardFull>('DELETE', `${cardPath(id)}/social-relations/${enc(name)}`),
  removeAsset: (id: number, name: string) => api<CharacterCardFull>('DELETE', `${cardPath(id)}/assets/${enc(name)}`),
  forgetSpell: (id: number, spell: string) => api<CharacterCardFull>('DELETE', `${cardPath(id)}/spells/${enc(spell)}`),

  warehouse: () => api<WarehouseResponse>('GET', '/api/warehouse'),
  deposit: (cardId: number, it: ItemRef, count?: number) =>
    api<WarehouseTransferResponse>('POST', '/api/warehouse/deposit', { character_card_id: cardId, item: itemBody(it), count }),
  withdraw: (cardId: number, it: ItemRef, count?: number) =>
    api<WarehouseTransferResponse>('POST', '/api/warehouse/withdraw', { character_card_id: cardId, item: itemBody(it), count }),
  discard: (it: ItemRef, count?: number) => api<WarehouseResponse>('DELETE', `/api/warehouse/items/${itemQuery(it, count)}`),

  announcements: () => api<Announcement[] | null>('GET', '/api/announcements'),
  recentSessions: (limit: number) => api<Page<RecentSession>>('GET', `/api/sessions?page=1&page_size=${limit}`),
}
