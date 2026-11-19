<script setup lang="ts">
import { MessageCircle, Send } from '@lucide/vue'
import { computed, nextTick, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { Session } from '@/api/types'
import BaseButton from '@/components/base/BaseButton.vue'
import type { OocMessage, Room } from '@/game/roomState'
import { useUiStore } from '@/stores/ui'
import { fmtTime } from '@/utils/format'

const props = defineProps<{
  session: Session | null
  room: Room | null
  userId: number | undefined
  canPost: boolean
  active: boolean
}>()

const ui = useUiStore()
const listEl = ref<HTMLElement | null>(null)
const draft = ref('')
const loading = ref(false)
const sending = ref(false)
const loadedSessionId = ref<number | null>(null)

const messages = computed(() => props.room?.ooc ?? [])

function mergeOoc(room: Room, items: OocMessage[]) {
  const existingIds = new Set(room.ooc.map((m) => m.id))
  for (const raw of items) {
    const id = Number(raw?.id)
    if (!Number.isFinite(id) || existingIds.has(id)) continue
    existingIds.add(id)
    const msg: OocMessage = {
      id,
      session_id: raw.session_id,
      user_id: Number(raw.user_id) || 0,
      username: String(raw.username || ''),
      character_name: raw.character_name ? String(raw.character_name) : undefined,
      content: String(raw.content || ''),
      created_at: raw.created_at,
    }
    let i = room.ooc.length
    while (i > 0 && room.ooc[i - 1].id > id) i--
    room.ooc.splice(i, 0, msg)
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
    const res = await api<unknown>('GET', `/api/sessions/${sid}/ooc`)
    if (Array.isArray(res)) {
      mergeOoc(room, res as OocMessage[])
      loadedSessionId.value = sid
      scrollToBottom()
    }
  } catch {
    // 静默容错：无权限或测试 mock 时不弹出干扰提示
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.active, props.session?.id] as const,
  ([active, sid]) => {
    if (!sid) {
      loadedSessionId.value = null
      return
    }
    if (active) {
      if (props.room) props.room.oocUnread = 0
      void loadHistory()
      scrollToBottom()
    }
  },
  { immediate: true },
)

watch(
  () => messages.value.length,
  () => {
    if (props.active) {
      if (props.room) props.room.oocUnread = 0
      scrollToBottom()
    }
  },
)

function senderLabel(m: OocMessage): string {
  return m.character_name ? `${m.character_name} · ${m.username}` : m.username || '玩家'
}

async function send() {
  const sid = props.session?.id
  const room = props.room
  const text = draft.value.trim()
  if (!sid || !room || !text || sending.value) return
  sending.value = true
  try {
    const created = await api<OocMessage>('POST', `/api/sessions/${sid}/ooc`, { content: text })
    draft.value = ''
    if (created && created.id != null) {
      mergeOoc(room, [created])
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
    <p class="border-b border-line px-4 py-2 text-xs text-fg-subtle">
      桌边频道用于玩家间商量战术与闲聊，不会进入 KP 剧情。
    </p>

    <div ref="listEl" class="min-h-0 flex-1 space-y-2.5 overflow-y-auto px-4 py-3">
      <p v-if="loading && !messages.length" class="py-6 text-center text-xs text-fg-subtle">加载中…</p>
      <div v-else-if="!messages.length" class="flex flex-col items-center gap-1.5 py-8 text-center text-xs text-fg-subtle">
        <MessageCircle :size="20" :stroke-width="1.5" />
        <span>还没有桌边讨论，发一句和队友打招呼吧。</span>
      </div>
      <div
        v-for="m in messages"
        :key="m.id"
        class="rounded-lg px-3 py-2"
        :class="m.user_id === userId ? 'bg-accent/15' : 'bg-surface-2'"
      >
        <div class="mb-0.5 flex items-baseline justify-between gap-2 text-xs">
          <span class="truncate font-medium" :class="m.user_id === userId ? 'text-accent' : 'text-fg-muted'">
            {{ senderLabel(m) }}
          </span>
          <span class="shrink-0 tabular-nums text-fg-subtle">{{ fmtTime(m.created_at) }}</span>
        </div>
        <p class="whitespace-pre-wrap break-words text-xs leading-relaxed text-fg">{{ m.content }}</p>
      </div>
    </div>

    <!-- NOTE: 这里刻意使用 input 而非 textarea，确保未轮到发言时页面里不存在额外的自由叙事输入框 -->
    <form v-if="canPost" class="flex shrink-0 items-center gap-2 border-t border-line p-3" @submit.prevent="send">
      <input
        v-model="draft"
        type="text"
        maxlength="500"
        placeholder="在桌边说点什么…"
        aria-label="桌边讨论输入"
        class="min-w-0 flex-1 rounded-lg border border-line bg-bg px-3 py-1.5 text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
      />
      <BaseButton type="submit" size="sm" :loading="sending" :disabled="!draft.trim()" aria-label="发送桌边消息">
        <Send :size="14" :stroke-width="1.75" />
      </BaseButton>
    </form>
  </div>
</template>
