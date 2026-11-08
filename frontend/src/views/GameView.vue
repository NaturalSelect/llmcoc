<script setup lang="ts">
import { Hourglass } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, postForm } from '@/api/client'
import { getToken } from '@/api/token'
import type { CharacterCardFull } from '@/api/characterTypes'
import type { CharacterCard, Session, SessionPlayer } from '@/api/types'
import StampBadge from '@/components/base/StampBadge.vue'
import CharacterDetailModal from '@/components/character/CharacterDetailModal.vue'
import AskCard from '@/components/game/AskCard.vue'
import GameHeader from '@/components/game/GameHeader.vue'
import GameHud from '@/components/game/GameHud.vue'
import GameSidebar from '@/components/game/GameSidebar.vue'
import ImagePreview from '@/components/game/ImagePreview.vue'
import InvestigatorListModal from '@/components/game/InvestigatorListModal.vue'
import MessageItem from '@/components/game/MessageItem.vue'
import StatusDock from '@/components/game/StatusDock.vue'
import { askView, createAskDraft, resetAskDraft, resetDraftOnFrame } from '@/game/ask'
import { showWaiting } from '@/game/banner'
import { diffHud, endSessionMessage, myCard, NO_FLASH, ownCardAccess, playerCardAccess } from '@/game/hud'
import type { HudFlash, HudSnapshot } from '@/game/hud'
import { currentGameTime } from '@/game/messages'
import { createRoomController } from '@/game/roomController'
import type { Frame } from '@/game/roomState'
import { useAuthStore } from '@/stores/auth'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'

const FLASH_MS = 700
const SCROLL_THRESHOLD = 120

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const chars = useCharactersStore()
const ui = useUiStore()

const draft = reactive(createAskDraft())
const chatBox = ref<HTMLElement | null>(null)
const investigatorsOpen = ref(false)
const previewSrc = ref('')
// NOTE: 点开的人物卡，由 CharacterDetailModal 展示；游戏页的 CharacterCard 类型只列了部分字段，弹窗要求完整卡类型（字段全为可选）
const viewCard = ref<CharacterCardFull | null>(null)
const busy = reactive({ leaving: false, ending: false, reviving: false })
const hudFlash = ref<HudFlash>(NO_FLASH)

let hudLast: HudSnapshot | null = null
let flashTimer: ReturnType<typeof setTimeout> | null = null
let scrollQueued = false

// 已在底部附近才跟随滚动，避免打断用户翻阅历史；force 用于首次进入和自己发言后
function scrollChat(force = false) {
  const el = chatBox.value
  if (!el) return
  const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < SCROLL_THRESHOLD
  if (force || atBottom) el.scrollTop = el.scrollHeight
}

function queueScroll() {
  if (scrollQueued) return
  scrollQueued = true
  requestAnimationFrame(() => {
    scrollQueued = false
    scrollChat()
  })
}

function onFrame(frame: Frame) {
  resetDraftOnFrame(draft, frame)
  if (frame.type === 'message_new' || frame.type === 'message_update' || frame.type === 'writer_token') queueScroll()
}

function authLost() {
  if (!auth.isLoggedIn) return
  const redirect = route.fullPath
  auth.logout()
  chars.reset()
  void router.replace({ name: 'login', query: { redirect } })
}

const controller = createRoomController({
  api,
  postForm,
  token: getToken,
  user: () => (auth.user ? { id: auth.user.id, role: auth.user.role } : null),
  toast: ui.toast,
  confirm: ui.confirm,
  onAuthLost: authLost,
  onRoomGone: () => void router.replace({ name: 'sessions' }),
  onFrame,
  onSnapshot: () => void nextTick(() => scrollChat(true)),
})
// NOTE: 控制器是 markRaw 的普通对象，模板不会自动解包它的属性，所以把两个 ref 取出来放在顶层
const { room, session } = controller

const user = computed(() => (auth.user ? { id: auth.user.id, role: auth.user.role } : null))
const card = computed(() => myCard(session.value, auth.user?.id))
const gameTime = computed(() => currentGameTime(room.value))
const waiting = computed(() => showWaiting(room.value))
const asking = computed(() => askView(room.value, draft))

async function enter(id: number) {
  resetAskDraft(draft)
  hudLast = null
  hudFlash.value = NO_FLASH
  await controller.enter(id)
  // NOTE: 进房失败（房间不存在或无权限）时 enter 已经弹过提示，回到大厅
  if (!room.value) void router.replace({ name: 'sessions' })
}

// HP/SAN 下降时闪一次高亮
watch(
  () => [session.value?.id, card.value] as const,
  ([sid, c]) => {
    const r = diffHud(hudLast, sid, c)
    hudLast = r.next
    if (!r.flash.hp && !r.flash.san) return
    hudFlash.value = r.flash
    if (flashTimer) clearTimeout(flashTimer)
    flashTimer = setTimeout(() => (hudFlash.value = NO_FLASH), FLASH_MS)
  },
)

// 问题卡出现时滚到底部，保证它不盖住最后一条消息
watch(asking, (v) => {
  if (v === 'card') void nextTick(() => scrollChat(true))
})

watch(
  () => route.params.id,
  (id, old) => {
    if (id && id !== old && route.name === 'game') void enter(Number(id))
  },
)

onMounted(() => {
  void enter(Number(route.params.id))
  if (!Object.keys(auth.shopCosts).length) void auth.loadShopCosts()
})

onBeforeUnmount(() => {
  controller.leave()
  if (flashTimer) clearTimeout(flashTimer)
})

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

async function submit(text: string) {
  if (await controller.sendChat(text)) {
    resetAskDraft(draft)
    void nextTick(() => scrollChat(true))
  }
}

async function leaveSession(s: Session) {
  if (!(await ui.confirm('确认退出该房间？', { danger: true, confirmText: '退出' }))) return
  busy.leaving = true
  try {
    const r = await api<{ message?: string }>('POST', '/api/sessions/' + s.id + '/leave')
    ui.toast(r.message || '已退出房间')
    await router.replace({ name: 'sessions' })
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    busy.leaving = false
  }
}

async function startSession(s: Session) {
  try {
    await api('POST', '/api/sessions/' + s.id + '/start')
    ui.toast('游戏已开始！')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
    return
  }
  // NOTE: 状态变化经 WebSocket 的 session 事件送达，不需要手工刷新；开场需要房主主动触发一次 KP
  await controller.openScene()
}

async function endSession(s: Session) {
  const cost = Number(auth.shopCosts.end_session_cost ?? 200)
  if (!(await ui.confirm(endSessionMessage(s.players?.length || 0, cost), { confirmText: '结束游戏' }))) return
  busy.ending = true
  try {
    const r = await api<{ message?: string }>('POST', '/api/sessions/' + s.id + '/end')
    ui.toast(r.message || '游戏已结束，角色成长已结算。')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    busy.ending = false
  }
}

async function reviveSession(s: Session) {
  if (!(await ui.confirm(`确认复活房间「${(s.name as string) || s.id}」？`, { confirmText: '复活' }))) return
  busy.reviving = true
  try {
    const r = await api<{ message?: string }>('POST', '/api/sessions/' + s.id + '/revive')
    ui.toast(r.message || '房间已复活')
    await controller.refreshSession()
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    busy.reviving = false
  }
}

function openCard(access: ReturnType<typeof ownCardAccess>) {
  if (access.ok) viewCard.value = access.card as CharacterCardFull
  else ui.toast(access.error, 'error')
}

function onAction(action: 'investigators' | 'myCard' | 'leave' | 'start' | 'end' | 'revive') {
  const s = session.value
  if (!s) return
  if (action === 'investigators') investigatorsOpen.value = true
  else if (action === 'myCard') openCard(ownCardAccess(s, user.value))
  else if (action === 'leave') void leaveSession(s)
  else if (action === 'start') void startSession(s)
  else if (action === 'end') void endSession(s)
  else void reviveSession(s)
}

function onSelectPlayer(p: SessionPlayer) {
  openCard(playerCardAccess(p, user.value))
}

defineExpose({ viewCard })
</script>

<template>
  <div class="flex min-h-0 flex-1 overflow-hidden bg-bg">
    <div class="mx-auto flex min-h-0 w-full max-w-7xl">
      <section class="flex min-h-0 min-w-0 flex-1 flex-col">
        <GameHeader v-if="session" :session="session" :user="user" :busy="busy" @action="onAction" />
        <GameHud v-if="card" class="lg:hidden" :card="card" :flash="hudFlash" :time="gameTime" />

        <div ref="chatBox" class="min-h-0 flex-1 overflow-y-auto" role="log" aria-live="off">
          <div class="mx-auto max-w-3xl space-y-6 px-4 py-6">
            <template v-if="room">
              <MessageItem v-for="m in room.messages" :key="m.id" :msg="m" :room="room" @preview="previewSrc = $event" />

              <div v-if="waiting" class="space-y-2 rounded-xl bg-surface-2 px-4 py-3">
                <p class="flex items-center gap-2 text-sm text-fg-muted">
                  <Hourglass :size="16" :stroke-width="1.75" class="shrink-0" />
                  等待其他调查员提交行动…
                </p>
                <div v-if="room.waiting.submittedNames.length" class="flex flex-wrap items-center gap-1.5">
                  <span class="shrink-0 text-xs text-fg-subtle">已提交：</span>
                  <StampBadge v-for="n in room.waiting.submittedNames" :key="n" tone="moss">{{ n }}</StampBadge>
                </div>
                <div v-if="room.waiting.pendingNames.length" class="flex flex-wrap items-center gap-1.5">
                  <span class="shrink-0 text-xs text-fg-subtle">待提交：</span>
                  <StampBadge v-for="n in room.waiting.pendingNames" :key="n" tone="faint">{{ n }}</StampBadge>
                </div>
              </div>
            </template>
          </div>
        </div>

        <div v-if="room" class="mx-auto flex w-full max-w-3xl shrink-0 flex-col gap-3 px-4 pb-4">
          <AskCard :room="room" :draft="draft" @submit="submit" />
          <StatusDock :room="room" @continue="controller.hostContinue()" @stop="controller.hostStop()" />
        </div>
      </section>

      <GameSidebar
        class="hidden w-80 shrink-0 flex-col overflow-y-auto border-l border-line bg-surface lg:flex"
        :session="session"
        :room="room"
        :card="card"
        :flash="hudFlash"
        :time="gameTime"
        :user-id="auth.user?.id"
        @select="onSelectPlayer"
      />
    </div>

    <InvestigatorListModal
      :open="investigatorsOpen"
      :session="session"
      :room="room"
      :user-id="auth.user?.id"
      @close="investigatorsOpen = false"
      @select="onSelectPlayer"
    />
    <ImagePreview :src="previewSrc" @close="previewSrc = ''" />
    <CharacterDetailModal :open="viewCard !== null" :card="viewCard" @close="viewCard = null" />
  </div>
</template>
