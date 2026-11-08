import { defineStore } from 'pinia'
import { ref } from 'vue'
import { fetchAllPages } from '@/api/pagination'
import type { CharacterCard } from '@/api/types'

// NOTE: 人物卡和阵亡人物卡列表会被顶栏（卡数）、首页、大厅加入房间、商城复活等多处直接复用，所以放在全局 store 里，
// 并且必须拉全量；onProgress 边拉边刷新界面
export const useCharactersStore = defineStore('characters', () => {
  const characters = ref<CharacterCard[]>([])
  const deadCharacters = ref<CharacterCard[]>([])
  const loading = ref(false)

  async function loadCharacters(): Promise<void> {
    loading.value = true
    try {
      characters.value = await fetchAllPages<CharacterCard>('/api/characters', {
        onProgress: (items) => {
          characters.value = items
        },
      })
    } finally {
      loading.value = false
    }
  }

  async function loadDeadCharacters(): Promise<void> {
    deadCharacters.value = await fetchAllPages<CharacterCard>('/api/characters/dead', {
      onProgress: (items) => {
        deadCharacters.value = items
      },
    })
  }

  // NOTE: 详情弹窗、商城、仓库等处改动人物卡后用返回的整张卡替换列表里的同 id 项；列表里没有（例如管理员看别人的卡）则不动
  function syncCharacter(updated: CharacterCard | null | undefined): void {
    if (!updated?.id) return
    characters.value = characters.value.map((c) => (c.id === updated.id ? updated : c))
  }

  function removeCharacter(id: number): void {
    characters.value = characters.value.filter((c) => c.id !== id)
  }

  function reset(): void {
    characters.value = []
    deadCharacters.value = []
  }

  return { characters, deadCharacters, loading, loadCharacters, loadDeadCharacters, syncCharacter, removeCharacter, reset }
})
