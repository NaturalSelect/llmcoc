<script setup lang="ts">
import { Hourglass } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, postForm } from '@/api/client'
import { getToken } from '@/api/token'
import type { CharacterCardFull } from '@/api/characterTypes'
import type { CharacterCard, Session, SessionPlayer } from '@/api/types'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import CharacterDetailModal from '@/components/character/CharacterDetailModal.vue'
import AskCard from '@/components/game/AskCard.vue'
import EncounterTracker from '@/components/game/EncounterTracker.vue'
import GameHeader from '@/components/game/GameHeader.vue'
import GameHud from '@/components/game/GameHud.vue'
import GameSidebar from '@/components/game/GameSidebar.vue'
import HandbookPanel from '@/components/game/HandbookPanel.vue'
import ImagePreview from '@/components/game/ImagePreview.vue'
import InvestigatorListModal from '@/components/game/InvestigatorListModal.vue'
import MessageItem from '@/components/game/MessageItem.vue'
import StatusDock from '@/components/game/StatusDock.vue'
import TableTalkPanel from '@/components/game/TableTalkPanel.vue'
import JoinSessionModal from '@/components/session/JoinSessionModal.vue'
import { askAnchor, askView, createAskDraft, resetAskDraft, resetDraftOnFrame } from '@/game/ask'
import { showWaiting } from '@/game/banner'
import { emptyHandbook, normalizeHandbook } from '@/game/handbookTypes'
import type { HandbookData } from '@/game/handbookTypes'
import { diffHud, endSessionMessage, isInSession, myCard, NO_FLASH, ownCardAccess, playerCardAccess } from '@/game/hud'
import type { HudFlash, HudSnapshot } from '@/game/hud'
import { currentGameTime, msgKind, writerText } from '@/game/messages'
import { createRoomController } from '@/game/roomController'
import type { Frame, RoomMessage } from '@/game/roomState'
import { formatTurnTitle, loadTurnSoundPref, playTurnChime, saveTurnSoundPref } from '@/game/turnAlert'
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
const joinOpen = ref(false)
const oocOpen = ref(false)
const handbookOpen = ref(false)
const handbook = ref<HandbookData>(emptyHandbook())
const handbookLoading = ref(false)
const pinOpen = ref(false)
const pinTitle = ref('')
const pinContent = ref('')
const pinSource = ref('')
const pinSaving = ref(false)
const soundEnabled = ref(loadTurnSoundPref())
const previewSrc = ref('')
// NOTE: 点开的人物卡，由 CharacterDetailModal 展示；游戏页的 CharacterCard 类型只列了部分字段，弹窗要求完整卡类型（字段全为可选）
const viewCard = ref<CharacterCardFull | null>(null)
const busy = reactive({ leaving: false, ending: false, reviving: false })
const hudFlash = ref<HudFlash>(NO_FLASH)
const originalTitle = typeof document !== 'undefined' ? document.title : ''

let hudLast: HudSnapshot | null = null
let flashTimer: ReturnType<typeof setTimeout> | null = null
let scrollQueued = false
// NOTE: 是否跟随最新消息只由用户的滚动动作决定，不能在内容增长之后再量：新内容一次长高超过阈值时，贴底的玩家会被误判成在翻历史
let stickToBottom = true
let initialScrollPending = true

function onChatScroll() {
  const el = chatBox.value
  if (!el) return
  stickToBottom = el.scrollHeight - el.scrollTop - el.clientHeight < SCROLL_THRESHOLD
}

// 玩家贴在底部才跟随滚动，避免打断翻阅历史；force 只用于进房后的首次定位和自己提交之后
function scrollChat(force = false) {
  const el = chatBox.value
  if (!el || (!force && !stickToBottom)) return
  el.scrollTop = el.scrollHeight
  stickToBottom = true
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
  if (frame.type === 'message_new' || frame.type === 'message_update' || frame.type === 'writer_token' || frame.type === 'illustration') queueScroll()
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
  onSnapshot: () =>
    void nextTick(() => {
      // NOTE: 重连会重新拉快照，只有进房后的第一份才强制定位到底部，之后遵循玩家当前的滚动位置
      scrollChat(initialScrollPending)
      initialScrollPending = false
    }),
})
// NOTE: 控制器是 markRaw 的普通对象，模板不会自动解包它的属性，所以把两个 ref 取出来放在顶层
const { room, session } = controller

const user = computed(() => (auth.user ? { id: auth.user.id, role: auth.user.role } : null))
const card = computed(() => myCard(session.value, auth.user?.id))
const gameTime = computed(() => currentGameTime(room.value))
const waiting = computed(() => showWaiting(room.value))
const asking = computed(() => askView(room.value, draft))
const askAt = computed(() => askAnchor(room.value))
const canJoin = computed(() => session.value?.status === 'lobby' && !isInSession(session.value, auth.user?.id))

async function enter(id: number) {
  resetAskDraft(draft)
  hudLast = null
  hudFlash.value = NO_FLASH
  stickToBottom = true
  initialScrollPending = true
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

// 问题卡出现时，贴在底部的玩家跟到卡片处；正在翻阅历史的玩家不被打断
watch(asking, (v) => {
  if (v === 'card') void nextTick(() => scrollChat())
})

const canPostOoc = computed(() =>
  Boolean(session.value && (isInSession(session.value, auth.user?.id) || session.value.created_by === auth.user?.id || auth.isAdmin)),
)

async function loadHandbook() {
  const sid = session.value?.id
  if (!sid) return
  handbookLoading.value = true
  try {
    const res = await api<unknown>('GET', `/api/sessions/${sid}/handbook`)
    handbook.value = normalizeHandbook(res)
  } catch {
    // NOTE: 后台同步失败时不打扰玩家阅读，保留上一次有效手册数据
  } finally {
    handbookLoading.value = false
  }
}

watch(
  () => [session.value?.id, session.value?.status] as const,
  ([sid, status], prev) => {
    if (!sid) {
      handbook.value = emptyHandbook()
      return
    }
    if (prev && prev[0] !== sid) handbook.value = emptyHandbook()
    if (status === 'playing' || status === 'ended') void loadHandbook()
  },
)

watch(
  () => (room.value ? room.value.handbookSeq + room.value.encounterSeq : 0),
  (seq, prev) => {
    if (seq > 0 && seq !== prev) void loadHandbook()
  },
)

watch(handbookOpen, (open) => {
  if (open) void loadHandbook()
})

function openPinClue(msg: RoomMessage) {
  const raw = msgKind(msg) === 'writer' && room.value ? writerText(room.value, msg) : msg.content || ''
  const plain = raw
    .replace(/!\[[^\]]*\]\([^)]*\)/g, '')
    .replace(/[*_`>#]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
  if (!plain) return
  pinTitle.value = plain.slice(0, 24) + (plain.length > 24 ? '…' : '')
  pinContent.value = plain.slice(0, 400)
  pinSource.value = msgKind(msg) === 'writer' ? '叙事摘录' : 'KP 旁白'
  pinOpen.value = true
}

async function savePinnedClue() {
  if (!session.value || pinSaving.value) return
  const title = pinTitle.value.trim()
  const content = pinContent.value.trim()
  if (!title || !content) return
  pinSaving.value = true
  try {
    await api('POST', `/api/sessions/${session.value.id}/clues`, {
      title,
      content,
      source: pinSource.value.trim(),
    })
    pinOpen.value = false
    ui.toast('已收入线索板')
    void loadHandbook()
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    pinSaving.value = false
  }
}

function toggleSound() {
  soundEnabled.value = !soundEnabled.value
  saveTurnSoundPref(soundEnabled.value)
}

watch(
  () => [Boolean(room.value?.spotlight.askedMe && room.value?.status === 'playing'), (session.value?.name as string) || ''] as const,
  ([myTurn, sessionName], prev) => {
    if (typeof document !== 'undefined') {
      const base = sessionName ? `${sessionName} · LLM-COC` : originalTitle
      document.title = formatTurnTitle(base, myTurn)
    }
    const wasMyTurn = prev ? prev[0] : false
    if (myTurn && !wasMyTurn && soundEnabled.value) {
      playTurnChime()
    }
  },
)

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
  if (typeof document !== 'undefined' && originalTitle) document.title = originalTitle
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

function onAction(action: 'investigators' | 'myCard' | 'join' | 'leave' | 'start' | 'end' | 'revive') {
  const s = session.value
  if (!s) return
  if (action === 'investigators') investigatorsOpen.value = true
  else if (action === 'join') joinOpen.value = true
  else if (action === 'myCard') openCard(ownCardAccess(s, user.value))
  else if (action === 'leave') void leaveSession(s)
  else if (action === 'start') void startSession(s)
  else if (action === 'end') void endSession(s)
  else void reviveSession(s)
}

function onSelectPlayer(p: SessionPlayer) {
  openCard(playerCardAccess(p, user.value))
}

function openJoinFromList() {
  investigatorsOpen.value = false
  joinOpen.value = true
}

defineExpose({ viewCard })
</script>

<template>
  <div class="flex min-h-0 flex-1 overflow-hidden bg-bg">
    <div class="mx-auto flex min-h-0 w-full max-w-7xl">
      <section class="flex min-h-0 min-w-0 flex-1 flex-col">
        <GameHeader
          v-if="session"
          :session="session"
          :user="user"
          :busy="busy"
          :sound-enabled="soundEnabled"
          :ooc-unread="room?.oocUnread ?? 0"
          @action="onAction"
          @toggle-sound="toggleSound"
          @open-ooc="oocOpen = true"
          @open-handbook="handbookOpen = true"
        />
        <GameHud v-if="card" class="lg:hidden" :card="card" :flash="hudFlash" :time="gameTime" />
        <EncounterTracker :combat="handbook.combat" :chase="handbook.chase" />

        <div ref="chatBox" class="min-h-0 flex-1 overflow-y-auto" role="log" aria-live="off" @scroll.passive="onChatScroll">
          <div class="mx-auto max-w-3xl space-y-6 px-4 py-6">
            <template v-if="room">
              <template v-for="m in room.messages" :key="m.id">
                <MessageItem :msg="m" :room="room" :can-pin-clue="canPostOoc" @preview="previewSrc = $event" @pin-clue="openPinClue" />
                <AskCard v-if="m.id === askAt" :room="room" :draft="draft" :card="card" @submit="submit" />
              </template>
              <AskCard v-if="asking && askAt === null" :room="room" :draft="draft" :card="card" show-question @submit="submit" />

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

        <div v-if="room" class="mx-auto w-full max-w-3xl shrink-0 px-4 pb-4">
          <StatusDock :room="room" @continue="controller.hostContinue()" @stop="controller.hostStop()" />
        </div>
      </section>

      <GameSidebar
        class="hidden w-80 shrink-0 flex-col overflow-hidden border-l border-line bg-surface lg:flex"
        :session="session"
        :room="room"
        :card="card"
        :flash="hudFlash"
        :time="gameTime"
        :user-id="auth.user?.id"
        :handbook="handbook"
        :handbook-loading="handbookLoading"
        @select="onSelectPlayer"
        @refresh="controller.refreshSession()"
        @refresh-handbook="loadHandbook()"
      />
    </div>

    <InvestigatorListModal
      :open="investigatorsOpen"
      :session="session"
      :room="room"
      :user-id="auth.user?.id"
      :can-join="canJoin"
      @close="investigatorsOpen = false"
      @select="onSelectPlayer"
      @join="openJoinFromList"
    />
    <BaseModal :open="oocOpen" title="桌边讨论" max-width="max-w-md" @close="oocOpen = false">
      <div class="-mx-5 -mb-5 flex h-96 flex-col">
        <TableTalkPanel :session="session" :room="room" :user-id="auth.user?.id" :can-post="canPostOoc" :active="oocOpen" />
      </div>
    </BaseModal>
    <BaseModal :open="handbookOpen" title="调查手册" max-width="max-w-md" @close="handbookOpen = false">
      <div class="-mx-5 -mb-5 flex h-96 flex-col">
        <HandbookPanel
          :session="session"
          :data="handbook"
          :loading="handbookLoading"
          :user-id="auth.user?.id"
          :can-edit="canPostOoc"
          @refresh="loadHandbook()"
        />
      </div>
    </BaseModal>
    <BaseModal :open="pinOpen" title="收入线索板" max-width="max-w-md" @close="pinOpen = false">
      <form class="space-y-3" @submit.prevent="savePinnedClue">
        <div class="space-y-1">
          <label class="block text-xs text-fg-muted" for="pin-clue-title">线索标题</label>
          <input
            id="pin-clue-title"
            v-model="pinTitle"
            type="text"
            maxlength="100"
            class="w-full rounded-lg border border-line bg-surface-2 px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
          />
        </div>
        <div class="space-y-1">
          <label class="block text-xs text-fg-muted" for="pin-clue-source">来源（选填）</label>
          <input
            id="pin-clue-source"
            v-model="pinSource"
            type="text"
            maxlength="100"
            class="w-full rounded-lg border border-line bg-surface-2 px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
          />
        </div>
        <div class="space-y-1">
          <label class="block text-xs text-fg-muted" for="pin-clue-content">线索内容</label>
          <input
            id="pin-clue-content"
            v-model="pinContent"
            type="text"
            maxlength="1000"
            class="w-full rounded-lg border border-line bg-surface-2 px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
          />
        </div>
        <div class="flex justify-end gap-2 pt-1">
          <BaseButton variant="ghost" size="sm" @click="pinOpen = false">取消</BaseButton>
          <BaseButton type="submit" size="sm" :loading="pinSaving" :disabled="!pinTitle.trim() || !pinContent.trim()">保存线索</BaseButton>
        </div>
      </form>
    </BaseModal>
    <JoinSessionModal :open="joinOpen" :session="session" @close="joinOpen = false" @joined="controller.refreshSession()" />
    <ImagePreview :src="previewSrc" @close="previewSrc = ''" />
    <CharacterDetailModal :open="viewCard !== null" :card="viewCard" @close="viewCard = null" @portrait="controller.refreshSession()" />
  </div>
</template>
