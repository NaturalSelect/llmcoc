<script setup lang="ts">
import { Check, Hand, Pencil, Quote } from '@lucide/vue'
import { computed, nextTick, ref } from 'vue'
import BaseButton from '@/components/base/BaseButton.vue'
import type { AskDraft } from '@/game/ask'
import {
  askCharCount,
  askLockNote,
  askLocked,
  askOptions,
  askTag,
  askView,
  CHAT_MAX_CHARS,
  cancelEdit,
  draftAnswer,
  insertDialogueQuotes,
  startEdit,
  toggleOption,
} from '@/game/ask'
import type { Room } from '@/game/roomState'

// NOTE: 卡片紧跟在点名消息下面时问题已经在上一条消息里，不再重复；放在消息流末尾兜底时才需要带上问题原文
const props = defineProps<{ room: Room; draft: AskDraft; showQuestion?: boolean }>()
const emit = defineEmits<{ submit: [text: string]; shown: [] }>()

const input = ref<HTMLTextAreaElement | null>(null)

const view = computed(() => askView(props.room, props.draft))
const locked = computed(() => askLocked(props.room))
const options = computed(() => askOptions(props.room))
const answer = computed(() => draftAnswer(props.room, props.draft))
const count = computed(() => askCharCount(answer.value))

function submit() {
  if (locked.value || !answer.value || count.value > CHAT_MAX_CHARS) return
  emit('submit', answer.value)
}

async function edit() {
  startEdit(props.room, props.draft)
  await nextTick()
  input.value?.focus()
}

async function quotes() {
  const el = input.value
  if (!el || locked.value) return
  const r = insertDialogueQuotes(props.draft.input, el.selectionStart, el.selectionEnd)
  props.draft.input = r.value
  await nextTick()
  el.focus()
  el.setSelectionRange(r.caret, r.caret)
}
</script>

<template>
  <section
    v-if="view === 'card'"
    class="space-y-3 rounded-2xl border border-accent/40 bg-surface p-4 shadow-panel"
    :class="locked ? 'opacity-80' : ''"
    aria-label="KP 点名"
  >
    <div class="flex items-center gap-2 text-sm font-medium text-accent">
      <Hand :size="16" :stroke-width="1.75" class="shrink-0" />
      {{ askTag(room) }}
    </div>
    <p v-if="showQuestion" class="line-clamp-3 whitespace-pre-wrap text-sm text-fg-muted">{{ room.spotlight.question || '请写下你的行动' }}</p>
    <p v-if="locked" class="text-xs text-fg-subtle">{{ askLockNote(room) }}</p>

    <div v-if="options.length > 0" class="space-y-2">
      <p class="text-xs text-fg-subtle">推荐行动，可多选，也可以只写自己的</p>
      <div class="flex flex-wrap gap-2">
        <button
          v-for="opt in options"
          :key="opt"
          type="button"
          class="inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-left text-sm transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-50"
          :class="draft.pick.includes(opt) ? 'border-accent bg-accent/15 text-accent' : 'border-line bg-surface-2 text-fg hover:border-line-strong hover:bg-surface-3'"
          :aria-pressed="draft.pick.includes(opt)"
          :disabled="locked"
          @click="toggleOption(room, draft, opt)"
        >
          <Check v-if="draft.pick.includes(opt)" :size="16" :stroke-width="1.75" class="shrink-0" aria-hidden="true" />{{ opt }}
        </button>
      </div>
    </div>

    <!-- NOTE: 这里不能用 BaseTextarea：需要拿到 textarea 本身的 ref 来做聚焦和光标选区处理，样式与它保持一致 -->
    <textarea
      ref="input"
      v-model="draft.input"
      rows="2"
      :disabled="locked"
      :placeholder="
        options.length
          ? '补充或自己写下行动 (Ctrl+Enter 提交)'
          : '以角色身份写下你的行动或台词，如：我推开门，「有人在吗？」 (Ctrl+Enter 提交)'
      "
      class="min-h-20 w-full resize-none rounded-lg border border-line bg-surface-2 px-3 py-2 text-sm leading-relaxed text-fg outline-none transition placeholder:text-fg-subtle focus:border-accent focus:ring-2 focus:ring-accent/30 disabled:cursor-not-allowed disabled:opacity-60"
      @keydown.ctrl.enter.prevent="submit"
    />

    <div class="flex items-center gap-2">
      <BaseButton variant="ghost" size="sm" :disabled="locked" title="插入台词括号；选中文字时会直接包住选中内容" @click="quotes">
        <Quote :size="16" :stroke-width="1.75" />
        「」台词
      </BaseButton>
      <BaseButton v-if="room.me.submitted" variant="ghost" size="sm" @click="cancelEdit(draft)">取消修改</BaseButton>
      <span v-if="count > 0" class="ml-auto text-xs tabular-nums" :class="count > CHAT_MAX_CHARS ? 'text-danger' : 'text-fg-subtle'">
        {{ count }}/{{ CHAT_MAX_CHARS }}
      </span>
      <BaseButton :class="count > 0 ? '' : 'ml-auto'" :disabled="locked || !answer || count > CHAT_MAX_CHARS" @click="submit">
        {{ room.me.submitted ? '重新提交' : '提交' }}
      </BaseButton>
    </div>
  </section>

  <section v-else-if="view === 'done'" class="space-y-1.5 rounded-xl bg-surface-2 px-4 py-3 text-sm">
    <div class="flex items-center justify-between gap-2">
      <span class="inline-flex items-center gap-1.5 text-success">
        <Check :size="16" :stroke-width="1.75" class="shrink-0" aria-hidden="true" />
        已提交你的行动
      </span>
      <BaseButton variant="ghost" size="sm" @click="edit">
        <Pencil :size="16" :stroke-width="1.75" />
        修改
      </BaseButton>
    </div>
    <p v-if="room.me.draft" class="line-clamp-2 whitespace-pre-wrap text-sm text-fg-muted">{{ room.me.draft }}</p>
  </section>
</template>
