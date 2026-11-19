<script setup lang="ts">
import { Lock, Mail, Send } from '@lucide/vue'
import { computed, nextTick, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { Session, SessionPlayer } from '@/api/types'
import BaseButton from '@/components/base/BaseButton.vue'
import type { PrivateNote, Room } from '@/game/roomState'
import { useUiStore } from '@/stores/ui'
import { fmtTime } from '@/utils/format'

const props = defineProps<{
  session: Session | null
  room: Room | null
  userId: number | undefined
  canSend: boolean
  active: boolean
}>()

const ui = useUiStore()
const listEl = ref<HTMLElement | null>(null)
const draft = ref('')
const recipientId = ref(0)
const loading = ref(false)
const sending = ref(false)
const loadedSessionId = ref<number | null>(null)

const notes = computed(() => props.room?.privateNotes ?? [])
const teammates = computed<SessionPlayer[]>(() =>
  (props.session?.players ?? []).filter((p) => p.user_id !== props.userId),
)

function mergeNotes(room: Room, items: PrivateNote[]) {
  const existingIds = new Set(room.privateNotes.map((n) => n.id))
  for (const raw of items) {
    const id = Number(raw?.id)
    if (!Number.isFinite(id) || existingIds.has(id)) continue
    existingIds.add(id)
    const note: PrivateNote = {
      id,
      session_id: raw.session_id,
      from_user_id: Number(raw.from_user_id) || 0,
      from_name: String(raw.from_name || ''),
      to_user_id: Number(raw.to_user_id) || 0,
      to_name: String(raw.to_name || (Number(raw.to_user_id) === 0 ? 'KP' : '')),
      content: String(raw.content || ''),
      delivered_to_kp: Boolean(raw.delivered_to_kp),
      created_at: raw.created_at,
    }
    let i = room.privateNotes.length
    while (i > 0 && room.privateNotes[i - 1].id > id) i--
    room.privateNotes.splice(i, 0, note)
  }
}

function scrollToBottom() {
  void nextTick(() => {
    const el = listEl.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

async function loadHistory() {
  const sid = props.session?.id
  const room = props.room
  if (!sid || !room || loading.value) return
  if (loadedSessionId.value === sid) return
  loading.value = true
  try {
    const res = await api<unknown>('GET', `/api/sessions/${sid}/private-notes`)
    if (Array.isArray(res)) {
      mergeNotes(room, res as PrivateNote[])
      loadedSessionId.value = sid
      scrollToBottom()
    }
  } catch {
    // NOTE: 静默容错，无权限或测试 mock 时不弹出干扰提示
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.active, props.session?.id] as const,
  ([active, sid]) => {
    if (!sid) {
      loadedSessionId.value = null
      recipientId.value = 0
      return
    }
    if (active) {
      if (props.room) props.room.privateNotesUnread = 0
      void loadHistory()
      scrollToBottom()
    }
  },
  { immediate: true },
)

watch(
  () => notes.value.length,
  () => {
    if (props.active) {
      if (props.room) props.room.privateNotesUnread = 0
      scrollToBottom()
    }
  },
)

function playerDisplayName(p: SessionPlayer): string {
  return p.character_card?.name || p.user?.username || `玩家 #${p.user_id}`
}

function noteHeader(n: PrivateNote): string {
  const from = n.from_user_id === props.userId ? '你' : n.from_name || '调查员'
  const to = n.to_user_id === 0 ? 'KP' : n.to_user_id === props.userId ? '你' : n.to_name || '调查员'
  if (n.from_user_id === props.userId && n.to_user_id === 0) return '悄悄发给 KP'
  return `${from} → ${to}`
}

function noteCardClass(n: PrivateNote): string {
  if (n.to_user_id === 0) return 'border border-info/30 bg-info/10'
  if (n.from_user_id === props.userId) return 'bg-accent/15'
  return 'bg-surface-2'
}

async function send() {
  const sid = props.session?.id
  const room = props.room
  const text = draft.value.trim()
  if (!sid || !room || !text || sending.value) return
  sending.value = true
  try {
    const created = await api<PrivateNote>('POST', `/api/sessions/${sid}/private-notes`, {
      to_user_id: recipientId.value,
      content: text,
    })
    draft.value = ''
    if (created && created.id != null) {
      mergeNotes(room, [created])
    }
    scrollToBottom()
  } catch (e) {
    ui.toast(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <p class="flex items-start gap-1.5 border-b border-line px-4 py-2 text-xs text-fg-subtle">
      <Lock :size="13" :stroke-width="1.75" class="mt-0.5 shrink-0 text-info" />
      <span>桌下纸条仅递条人、收条人与 KP 可见；所有纸条都会在下个 KP 周期抄送 KP，结团复盘时揭晓。</span>
    </p>

    <div ref="listEl" class="min-h-0 flex-1 space-y-2.5 overflow-y-auto px-4 py-3">
      <p v-if="loading && !notes.length" class="py-6 text-center text-xs text-fg-subtle">加载中…</p>
      <div v-else-if="!notes.length" class="flex flex-col items-center gap-1.5 py-8 text-center text-xs text-fg-subtle">
        <Mail :size="20" :stroke-width="1.5" />
        <span>还没有桌下纸条，可以悄悄给 KP 或队友递一张。</span>
      </div>
      <div v-for="n in notes" :key="n.id" class="rounded-lg px-3 py-2" :class="noteCardClass(n)">
        <div class="mb-0.5 flex items-baseline justify-between gap-2 text-xs">
          <span class="truncate font-medium" :class="n.to_user_id === 0 ? 'text-info' : n.from_user_id === userId ? 'text-accent' : 'text-fg-muted'">
            {{ noteHeader(n) }}
          </span>
          <span class="shrink-0 tabular-nums text-fg-subtle">{{ fmtTime(n.created_at) }}</span>
        </div>
        <p class="whitespace-pre-wrap break-words text-xs leading-relaxed text-fg">{{ n.content }}</p>
      </div>
    </div>

    <!-- NOTE: 使用 input 而非 textarea，确保未轮到发言时页面里不存在额外的自由叙事输入框 -->
    <form v-if="canSend" class="shrink-0 space-y-2 border-t border-line p-3" @submit.prevent="send">
      <label class="flex items-center gap-2">
        <span class="shrink-0 text-xs text-fg-subtle">递给</span>
        <select
          v-model.number="recipientId"
          aria-label="选择纸条接收人"
          class="min-w-0 flex-1 rounded-lg border border-line bg-bg px-2.5 py-1 text-xs text-fg focus:border-accent focus:outline-none"
        >
          <option :value="0">KP（仅你与 KP 可见）</option>
          <option v-for="p in teammates" :key="p.user_id" :value="p.user_id">{{ playerDisplayName(p) }}</option>
        </select>
      </label>
      <div class="flex items-center gap-2">
        <input
          v-model="draft"
          type="text"
          maxlength="500"
          placeholder="写下要悄悄递出的纸条…"
          aria-label="桌下纸条内容"
          class="min-w-0 flex-1 rounded-lg border border-line bg-bg px-3 py-1.5 text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
        />
        <BaseButton type="submit" size="sm" :loading="sending" :disabled="!draft.trim()" aria-label="递出纸条">
          <Send :size="14" :stroke-width="1.75" />
        </BaseButton>
      </div>
    </form>
  </div>
</template>
