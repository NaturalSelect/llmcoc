<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Skull, Warehouse } from '@lucide/vue'
import type { Announcement, CharacterCardFull, RecentSession } from '@/api/characterTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PageHeader from '@/components/base/PageHeader.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import Skeleton from '@/components/base/Skeleton.vue'
import AnnouncementList from '@/components/character/AnnouncementList.vue'
import CharacterCardTile from '@/components/character/CharacterCardTile.vue'
import CharacterDetailModal from '@/components/character/CharacterDetailModal.vue'
import CreateCharacterModal from '@/components/character/CreateCharacterModal.vue'
import DeadCharacterCard from '@/components/character/DeadCharacterCard.vue'
import RecentSessions from '@/components/character/RecentSessions.vue'
import WarehouseModal from '@/components/character/WarehouseModal.vue'
import { characterApi } from '@/components/character/characterApi'
import { deleteDeadConfirmMessage, errMessage, reviveConfirmMessage, reviveCost } from '@/components/character/characterLogic'
import { RECENT_SESSION_LIMIT } from '@/components/character/dashboardLogic'
import { useAuthStore } from '@/stores/auth'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'

const auth = useAuthStore()
const chars = useCharactersStore()
const ui = useUiStore()
const router = useRouter()

const cards = computed(() => chars.characters as CharacterCardFull[])
const deadCards = computed(() => chars.deadCharacters as CharacterCardFull[])
const cost = computed(() => reviveCost(auth.shopCosts))

const loaded = ref(false)
const reviving = ref(false)
const announcements = ref<Announcement[]>([])
const sessions = ref<RecentSession[]>([])
const sessionsLoading = ref(false)

const detailOpen = ref(false)
const detailCard = ref<CharacterCardFull | null>(null)
const createOpen = ref(false)
const warehouseOpen = ref(false)

const showSkeleton = computed(() => (chars.loading || !loaded.value) && cards.value.length === 0)
const showEmpty = computed(() => loaded.value && !chars.loading && cards.value.length === 0)

function openDetail(card: CharacterCardFull): void {
  detailCard.value = card
  detailOpen.value = true
}

async function loadSessions(): Promise<void> {
  sessionsLoading.value = true
  try {
    sessions.value = (await characterApi.recentSessions(RECENT_SESSION_LIMIT)).items || []
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    sessionsLoading.value = false
  }
}

async function loadCards(): Promise<void> {
  try {
    await Promise.all([chars.loadCharacters(), chars.loadDeadCharacters()])
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    loaded.value = true
  }
}

// NOTE: 公告拉取失败不打扰用户，页面照常显示
async function loadAnnouncements(): Promise<void> {
  try {
    announcements.value = (await characterApi.announcements()) || []
  } catch {
    announcements.value = []
  }
}

async function deleteCard(card: CharacterCardFull): Promise<void> {
  if (!card.id) return
  if (!(await ui.confirm('确认删除此人物卡？', { danger: true, confirmText: '删除' }))) return
  try {
    await characterApi.remove(card.id)
    chars.removeCharacter(card.id)
    await chars.loadDeadCharacters()
    ui.toast('人物卡已删除')
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  }
}

async function deleteDead(card: CharacterCardFull): Promise<void> {
  if (!card.id) return
  if (!(await ui.confirm(deleteDeadConfirmMessage(card.name), { danger: true, confirmText: '彻底删除' }))) return
  try {
    await characterApi.removeDead(card.id)
    chars.deadCharacters = chars.deadCharacters.filter((c) => c.id !== card.id)
    ui.toast(`${card.name} 已彻底消逝……`)
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  }
}

async function revive(card: CharacterCardFull): Promise<void> {
  if (!card.id) return
  if (!(await ui.confirm(reviveConfirmMessage(cost.value, card.name), { confirmText: '复活' }))) return
  reviving.value = true
  try {
    const r = await characterApi.revive(card.id)
    if (auth.user && typeof r.coins === 'number') auth.user.coins = r.coins
    chars.characters = [...chars.characters, r.character_card]
    chars.deadCharacters = chars.deadCharacters.filter((c) => c.id !== card.id)
    ui.toast(`${card.name} 已复活！但部分记忆与装备永久消散……`)
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    reviving.value = false
  }
}

onMounted(() => {
  if (!Object.keys(auth.shopCosts).length) void auth.loadShopCosts()
  void loadCards()
  void loadAnnouncements()
  void loadSessions()
})
</script>

<template>
  <div class="mx-auto w-full max-w-6xl space-y-6 p-4 sm:p-6">
    <PageHeader title="我的调查员">
      <template #actions>
        <BaseButton variant="secondary" @click="warehouseOpen = true">
          <Warehouse :size="16" :stroke-width="1.75" />
          仓库
        </BaseButton>
        <BaseButton @click="createOpen = true">+ 创建角色</BaseButton>
      </template>
    </PageHeader>

    <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
      <section class="space-y-6 lg:col-span-2" aria-label="我的调查员">
        <div v-if="showSkeleton" class="grid grid-cols-1 gap-3 md:grid-cols-2" aria-busy="true">
          <PaperCard v-for="i in 4" :key="i">
            <div class="mb-4 flex items-center gap-3">
              <Skeleton class="h-11 w-11 rounded-full" />
              <Skeleton class="h-4 w-1/2" />
            </div>
            <div class="mb-3 grid grid-cols-4 gap-2">
              <Skeleton v-for="j in 4" :key="j" class="h-12" />
            </div>
            <div class="space-y-2">
              <Skeleton class="h-2 w-full" />
              <Skeleton class="h-2 w-full" />
              <Skeleton class="h-2 w-2/3" />
            </div>
          </PaperCard>
        </div>

        <EmptyState v-if="showEmpty">还没有调查员档案，创建第一张人物卡开始冒险！</EmptyState>

        <div v-if="cards.length > 0" class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <CharacterCardTile v-for="c in cards" :key="c.id" :card="c" @open="openDetail" @delete="deleteCard" />
        </div>

        <section v-if="deadCards.length > 0" aria-labelledby="dead-title">
          <SectionHeading id="dead-title" class="mb-3" :count="deadCards.length">
            <Skull :size="16" :stroke-width="1.75" class="text-fg-subtle" />
            已阵亡的调查员
          </SectionHeading>
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
            <DeadCharacterCard
              v-for="c in deadCards"
              :key="c.id"
              :card="c"
              :cost="cost"
              :coins="auth.user?.coins ?? 0"
              :reviving="reviving"
              @open="openDetail"
              @delete="deleteDead"
              @revive="revive"
            />
          </div>
        </section>
      </section>

      <aside class="space-y-6">
        <AnnouncementList :announcements="announcements" />
        <RecentSessions
          :sessions="sessions"
          :loading="sessionsLoading"
          @open="(id) => router.push({ name: 'game', params: { id } })"
          @all="router.push({ name: 'sessions' })"
        />
      </aside>
    </div>

    <CharacterDetailModal :open="detailOpen" :card="detailCard" @close="detailOpen = false" />
    <CreateCharacterModal :open="createOpen" @close="createOpen = false" />
    <WarehouseModal :open="warehouseOpen" @close="warehouseOpen = false" />
  </div>
</template>
