<script setup lang="ts">
import { onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import type { LobbySession } from '@/api/sessionTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import TabBar from '@/components/base/TabBar.vue'
import CreateSessionModal from '@/components/session/CreateSessionModal.vue'
import JoinSessionModal from '@/components/session/JoinSessionModal.vue'
import SessionCard from '@/components/session/SessionCard.vue'
import SessionListPanel from '@/components/session/SessionListPanel.vue'
import { errorMessage, isInSession, pageAfterRemoval } from '@/components/session/sessionLogic'
import { usePagedList } from '@/components/session/usePagedList'
import { useSessionActions } from '@/components/session/useSessionActions'
import { useAuthStore } from '@/stores/auth'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'

type Tab = 'lobby' | 'history' | 'favorites'

const TABS: Array<{ key: Tab; label: string }> = [
  { key: 'lobby', label: '活跃房间' },
  { key: 'history', label: '我的历史记录' },
  { key: 'favorites', label: '我的收藏' },
]

const router = useRouter()
const auth = useAuthStore()
const chars = useCharactersStore()
const ui = useUiStore()
const actions = useSessionActions()

const tab = ref<string>('lobby')
const createOpen = ref(false)
const joinOpen = ref(false)
const joinTarget = ref<LobbySession | null>(null)

// NOTE: reactive 包一层后模板里直接用 lobby.items / lobby.page，不用手动 .value
const lobby = reactive(usePagedList<LobbySession>('/api/sessions', 20, true))
const history = reactive(usePagedList<LobbySession>('/api/sessions/my-history'))
const favorites = reactive(usePagedList<LobbySession>('/api/sessions/my-favorites'))

function fail(e: unknown) {
  ui.toast(errorMessage(e), 'error')
}

// 进入房间现在是路由跳转，房间页自己拉取房间详情并建立连接
function enter(id: number) {
  void router.push('/sessions/' + id)
}

function openJoin(s: LobbySession) {
  joinTarget.value = s
  joinOpen.value = true
}

function onCreated(s: LobbySession) {
  lobby.prepend(s)
}

// 切到历史/收藏标签时总是回到第一页重新拉取，与旧前端一致
watch(tab, (t) => {
  if (t === 'history') history.load(1).catch(fail)
  else if (t === 'favorites') favorites.load(1).catch(fail)
})

function leave(s: LobbySession) {
  return actions.leaveSession(s, () => lobby.load())
}

function revive(s: LobbySession) {
  return actions.reviveSession(s, async () => {
    await Promise.all([lobby.load(), history.load(), favorites.load()])
  })
}

function addFavorite(id: number) {
  return actions.addFavorite(id, () => favorites.load(1))
}

// 取消收藏后若当前页被清空，回退一页再刷新
function removeFavorite(id: number) {
  return actions.removeFavorite(id, () => favorites.load(pageAfterRemoval(favorites.items.length, favorites.page)))
}

onMounted(() => {
  // NOTE: NSFW 开关来自公开设置，登录页之外的入口（刷新直达大厅）也要拉取一次
  void auth.loadPublicSettings()
  lobby.load().catch(fail)
  chars.loadCharacters().catch(fail)
})
</script>

<template>
  <div class="mx-auto w-full max-w-4xl p-5">
    <header class="mb-4 flex items-end justify-between border-b border-paper-edge pb-2">
      <h2 class="font-serif text-xl font-bold text-ink">跑团大厅</h2>
      <BaseButton @click="createOpen = true">+ 创建房间</BaseButton>
    </header>

    <TabBar v-model="tab" :tabs="TABS" class="mb-4" />

    <div v-show="tab === 'lobby'" role="tabpanel" aria-label="活跃房间">
      <SessionListPanel
        :items="lobby.items"
        :loading="lobby.loading"
        :page="lobby.page"
        :total-pages="lobby.totalPages"
        :total="lobby.total"
        empty-icon="🏚"
        empty-text="暂无活跃房间，来创建一个吧！"
        @prev="lobby.prev().catch(fail)"
        @next="lobby.next().catch(fail)"
      >
        <template #item="{ item: s }">
          <SessionCard :session="s">
            <template #actions>
              <BaseButton v-if="s.status === 'lobby' && !isInSession(s, auth.user?.id)" class="!px-3 !py-1.5" @click="openJoin(s)">加入</BaseButton>
              <BaseButton
                v-if="s.status === 'lobby' && isInSession(s, auth.user?.id)"
                variant="danger"
                class="!px-3 !py-1.5"
                :disabled="actions.leaving.value"
                @click="leave(s)"
              >
                退出
              </BaseButton>
              <BaseButton variant="ghost" class="!px-3 !py-1.5" @click="enter(s.id)">进入</BaseButton>
            </template>
          </SessionCard>
        </template>
      </SessionListPanel>
    </div>

    <div v-show="tab === 'history'" role="tabpanel" aria-label="我的历史记录">
      <SessionListPanel
        :items="history.items"
        :loading="history.loading"
        :page="history.page"
        :total-pages="history.totalPages"
        :total="history.total"
        empty-icon="📜"
        empty-text="还没有参与过已结束的房间"
        @prev="history.prev().catch(fail)"
        @next="history.next().catch(fail)"
      >
        <template #item="{ item: s }">
          <SessionCard :session="s" variant="archive">
            <template #actions>
              <BaseButton variant="ghost" class="!px-3 !py-1.5" @click="enter(s.id)">查看记录</BaseButton>
              <BaseButton variant="ghost" class="!px-3 !py-1.5" @click="addFavorite(s.id)">☆ 收藏</BaseButton>
              <BaseButton v-if="auth.isAdmin" variant="success" class="!px-3 !py-1.5" :disabled="actions.reviving.value" @click="revive(s)">复活</BaseButton>
            </template>
          </SessionCard>
        </template>
      </SessionListPanel>
    </div>

    <div v-show="tab === 'favorites'" role="tabpanel" aria-label="我的收藏">
      <SessionListPanel
        :items="favorites.items"
        :loading="favorites.loading"
        :page="favorites.page"
        :total-pages="favorites.totalPages"
        :total="favorites.total"
        empty-icon="🌟"
        empty-text="还没有收藏任何房间"
        @prev="favorites.prev().catch(fail)"
        @next="favorites.next().catch(fail)"
      >
        <template #item="{ item: s }">
          <SessionCard :session="s" variant="archive">
            <template #actions>
              <BaseButton variant="ghost" class="!px-3 !py-1.5" @click="enter(s.id)">查看记录</BaseButton>
              <BaseButton class="!px-3 !py-1.5" @click="removeFavorite(s.id)">★ 取消收藏</BaseButton>
              <BaseButton v-if="auth.isAdmin" variant="success" class="!px-3 !py-1.5" :disabled="actions.reviving.value" @click="revive(s)">复活</BaseButton>
            </template>
          </SessionCard>
        </template>
      </SessionListPanel>
    </div>

    <CreateSessionModal :open="createOpen" @close="createOpen = false" @created="onCreated" />
    <JoinSessionModal :open="joinOpen" :session="joinTarget" @close="joinOpen = false" @joined="lobby.load().catch(fail)" />
  </div>
</template>
