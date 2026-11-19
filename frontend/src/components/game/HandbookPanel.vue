<script setup lang="ts">
import { BookMarked, Compass, Lock, MapPin, Plus, Search, StickyNote, Trash2, Users } from '@lucide/vue'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { Session } from '@/api/types'
import BaseButton from '@/components/base/BaseButton.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import type { HandbookData, SessionClue, SessionNote } from '@/game/handbookTypes'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'

const props = defineProps<{
  session: Session | null
  data: HandbookData
  loading: boolean
  userId: number | undefined
  canEdit: boolean
}>()
const emit = defineEmits<{ refresh: [] }>()

const auth = useAuthStore()
const ui = useUiStore()

const subTab = ref<'clues' | 'notes' | 'world'>('clues')

const clueTitle = ref('')
const clueContent = ref('')
const clueSource = ref('')
const clueSaving = ref(false)

const noteContent = ref('')
const notePrivate = ref(false)
const noteSaving = ref(false)

const isHostOrAdmin = computed(() => Boolean(auth.isAdmin || (props.session && props.session.created_by === props.userId)))

function canDeleteClue(c: SessionClue): boolean {
  return Boolean(isHostOrAdmin.value || (c.user_id != null && c.user_id === props.userId))
}

function canDeleteNote(n: SessionNote): boolean {
  return Boolean(isHostOrAdmin.value || n.user_id === props.userId)
}

async function addClue() {
  if (!props.session || clueSaving.value) return
  const title = clueTitle.value.trim()
  const content = clueContent.value.trim()
  if (!title || !content) return
  clueSaving.value = true
  try {
    await api('POST', `/api/sessions/${props.session.id}/clues`, {
      title,
      content,
      source: clueSource.value.trim(),
    })
    clueTitle.value = ''
    clueContent.value = ''
    clueSource.value = ''
    emit('refresh')
  } catch (e) {
    ui.toast(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    clueSaving.value = false
  }
}

async function removeClue(id: number) {
  if (!props.session) return
  try {
    await api('DELETE', `/api/sessions/${props.session.id}/clues/${id}`)
    emit('refresh')
  } catch (e) {
    ui.toast(e instanceof Error ? e.message : String(e), 'error')
  }
}

async function addNote() {
  if (!props.session || noteSaving.value) return
  const content = noteContent.value.trim()
  if (!content) return
  noteSaving.value = true
  try {
    await api('POST', `/api/sessions/${props.session.id}/notes`, {
      content,
      is_shared: !notePrivate.value,
    })
    noteContent.value = ''
    emit('refresh')
  } catch (e) {
    ui.toast(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    noteSaving.value = false
  }
}

async function removeNote(id: number) {
  if (!props.session) return
  try {
    await api('DELETE', `/api/sessions/${props.session.id}/notes/${id}`)
    emit('refresh')
  } catch (e) {
    ui.toast(e instanceof Error ? e.message : String(e), 'error')
  }
}
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <div class="flex shrink-0 gap-1 border-b border-line px-3 py-2">
      <button
        type="button"
        class="inline-flex flex-1 items-center justify-center gap-1 rounded-md px-2 py-1.5 text-xs font-medium transition"
        :class="subTab === 'clues' ? 'bg-surface-2 text-accent' : 'text-fg-subtle hover:text-fg'"
        @click="subTab = 'clues'"
      >
        <Search :size="13" :stroke-width="1.75" class="shrink-0" />
        线索
        <span v-if="data.clues.length" class="tabular-nums text-fg-subtle">{{ data.clues.length }}</span>
      </button>
      <button
        type="button"
        class="inline-flex flex-1 items-center justify-center gap-1 rounded-md px-2 py-1.5 text-xs font-medium transition"
        :class="subTab === 'notes' ? 'bg-surface-2 text-accent' : 'text-fg-subtle hover:text-fg'"
        @click="subTab = 'notes'"
      >
        <StickyNote :size="13" :stroke-width="1.75" class="shrink-0" />
        笔记
        <span v-if="data.notes.length" class="tabular-nums text-fg-subtle">{{ data.notes.length }}</span>
      </button>
      <button
        type="button"
        class="inline-flex flex-1 items-center justify-center gap-1 rounded-md px-2 py-1.5 text-xs font-medium transition"
        :class="subTab === 'world' ? 'bg-surface-2 text-accent' : 'text-fg-subtle hover:text-fg'"
        @click="subTab = 'world'"
      >
        <Compass :size="13" :stroke-width="1.75" class="shrink-0" />
        见闻
      </button>
    </div>

    <div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
      <p v-if="loading && !data.clues.length && !data.notes.length && !data.locations.length" class="text-center text-xs text-fg-subtle">
        正在整理调查手册…
      </p>

      <template v-else-if="subTab === 'clues'">
        <form v-if="canEdit" class="space-y-2 rounded-lg border border-line bg-surface-2 p-2.5" @submit.prevent="addClue">
          <div class="flex gap-1.5">
            <input
              v-model="clueTitle"
              type="text"
              maxlength="100"
              placeholder="线索标题"
              aria-label="线索标题"
              class="min-w-0 flex-1 rounded-md border border-line bg-surface px-2.5 py-1.5 text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
            />
            <input
              v-model="clueSource"
              type="text"
              maxlength="100"
              placeholder="来源(选填)"
              aria-label="线索来源"
              class="w-24 rounded-md border border-line bg-surface px-2 py-1.5 text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
            />
          </div>
          <div class="flex gap-1.5">
            <input
              v-model="clueContent"
              type="text"
              maxlength="1000"
              placeholder="记下关键事实或摘录…"
              aria-label="线索内容"
              class="min-w-0 flex-1 rounded-md border border-line bg-surface px-2.5 py-1.5 text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
            />
            <BaseButton type="submit" size="sm" :loading="clueSaving" :disabled="!clueTitle.trim() || !clueContent.trim()">
              <Plus :size="14" :stroke-width="1.75" />
            </BaseButton>
          </div>
        </form>

        <p v-if="!data.clues.length" class="py-4 text-center text-xs text-fg-subtle">
          暂无线索。KP 揭示关键线索或点击叙事上的“收线索”都会汇总到这里。
        </p>

        <ul v-else class="space-y-2">
          <li v-for="c in data.clues" :key="c.id" class="rounded-lg border border-line bg-surface-2 px-3 py-2.5">
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0 flex-1">
                <p class="text-xs font-semibold text-fg">{{ c.title }}</p>
                <p class="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-fg-subtle">
                  <span>{{ c.added_by || 'KP' }}</span>
                  <span v-if="c.source">· {{ c.source }}</span>
                </p>
              </div>
              <button
                v-if="canDeleteClue(c)"
                type="button"
                class="rounded-md p-1 text-fg-subtle transition hover:bg-surface-3 hover:text-danger"
                aria-label="删除线索"
                @click="removeClue(c.id)"
              >
                <Trash2 :size="13" :stroke-width="1.75" />
              </button>
            </div>
            <p class="mt-1.5 whitespace-pre-wrap break-words text-xs leading-relaxed text-fg-muted">{{ c.content }}</p>
          </li>
        </ul>
      </template>

      <template v-else-if="subTab === 'notes'">
        <form v-if="canEdit" class="space-y-2 rounded-lg border border-line bg-surface-2 p-2.5" @submit.prevent="addNote">
          <div class="flex gap-1.5">
            <input
              v-model="noteContent"
              type="text"
              maxlength="1000"
              placeholder="写下你的推理或备忘…"
              aria-label="笔记内容"
              class="min-w-0 flex-1 rounded-md border border-line bg-surface px-2.5 py-1.5 text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
            />
            <BaseButton type="submit" size="sm" :loading="noteSaving" :disabled="!noteContent.trim()">
              <Plus :size="14" :stroke-width="1.75" />
            </BaseButton>
          </div>
          <label class="inline-flex cursor-pointer items-center gap-1.5 text-xs text-fg-muted">
            <input v-model="notePrivate" type="checkbox" class="accent-accent" />
            <Lock :size="12" :stroke-width="1.75" class="text-info" />
            仅自己可见（私密笔记）
          </label>
        </form>

        <p v-if="!data.notes.length" class="py-4 text-center text-xs text-fg-subtle">暂无调查笔记。</p>

        <ul v-else class="space-y-2">
          <li
            v-for="n in data.notes"
            :key="n.id"
            class="rounded-lg border px-3 py-2"
            :class="n.is_shared ? 'border-line bg-surface-2' : 'border-info/30 bg-info/10'"
          >
            <div class="flex items-center justify-between gap-2 text-xs">
              <span class="inline-flex items-center gap-1.5 font-medium" :class="n.is_shared ? 'text-fg' : 'text-info'">
                <Lock v-if="!n.is_shared" :size="12" :stroke-width="1.75" />
                {{ n.author_name }}
                <span class="font-normal text-fg-subtle">{{ n.is_shared ? '团队共享' : '仅自己可见' }}</span>
              </span>
              <button
                v-if="canDeleteNote(n)"
                type="button"
                class="rounded-md p-1 text-fg-subtle transition hover:bg-surface-3 hover:text-danger"
                aria-label="删除笔记"
                @click="removeNote(n.id)"
              >
                <Trash2 :size="13" :stroke-width="1.75" />
              </button>
            </div>
            <p class="mt-1 whitespace-pre-wrap break-words text-xs leading-relaxed text-fg-muted">{{ n.content }}</p>
          </li>
        </ul>
      </template>

      <template v-else>
        <section class="space-y-2">
          <p class="flex items-center gap-1.5 text-xs font-medium text-fg-muted">
            <MapPin :size="13" :stroke-width="1.75" class="text-accent" />
            已到访地点 ({{ data.locations.length }})
          </p>
          <p v-if="!data.locations.length" class="text-xs text-fg-subtle">尚未记录到访地点。</p>
          <ul v-else class="space-y-1.5">
            <li
              v-for="loc in data.locations"
              :key="loc.name"
              class="flex flex-wrap items-center justify-between gap-1.5 rounded-lg border border-line bg-surface-2 px-3 py-2 text-xs"
            >
              <span class="font-medium text-fg">{{ loc.name }}</span>
              <div v-if="loc.occupants.length" class="flex flex-wrap gap-1">
                <StampBadge v-for="o in loc.occupants" :key="o" tone="moss">{{ o }}</StampBadge>
              </div>
            </li>
          </ul>
        </section>

        <section class="space-y-2">
          <p class="flex items-center gap-1.5 text-xs font-medium text-fg-muted">
            <Users :size="13" :stroke-width="1.75" class="text-accent" />
            已遇人物 ({{ data.npcs.length }})
          </p>
          <p v-if="!data.npcs.length" class="text-xs text-fg-subtle">尚未记录已登场人物。</p>
          <ul v-else class="space-y-1.5">
            <li
              v-for="npc in data.npcs"
              :key="npc.name"
              class="rounded-lg border border-line bg-surface-2 px-3 py-2 text-xs"
            >
              <div class="flex items-center justify-between gap-2">
                <span class="font-medium" :class="npc.is_alive ? 'text-fg' : 'text-fg-subtle line-through'">{{ npc.name }}</span>
                <StampBadge v-if="!npc.is_alive" tone="seal">已故</StampBadge>
              </div>
              <p v-if="npc.occupation || npc.location" class="mt-0.5 text-fg-subtle">
                <span v-if="npc.occupation">{{ npc.occupation }}</span>
                <span v-if="npc.occupation && npc.location"> · </span>
                <span v-if="npc.location">{{ npc.location }}</span>
              </p>
            </li>
          </ul>
        </section>

        <p class="flex items-center gap-1.5 text-xs text-fg-subtle">
          <BookMarked :size="12" :stroke-width="1.75" class="shrink-0" />
          仅记录桌上公开见闻，不含未触发设定。
        </p>
      </template>
    </div>
  </div>
</template>
