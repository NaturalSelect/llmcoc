<script setup lang="ts">
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

const props = defineProps<{ room: Room; draft: AskDraft }>()
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
    class="shrink-0 space-y-2 border-t-2 border-seal bg-paper px-3 py-3"
    :class="locked ? 'opacity-80' : ''"
    aria-label="KP 点名"
  >
    <div class="font-type text-xs font-bold tracking-wide text-seal">👉 {{ askTag(room) }}</div>
    <p class="whitespace-pre-wrap text-sm leading-relaxed text-ink">{{ room.spotlight.question || '请写下你的行动' }}</p>
    <p v-if="locked" class="text-xs text-ink-faint">{{ askLockNote(room) }}</p>

    <div v-if="options.length > 0">
      <p class="mb-1 text-xs text-ink-faint">推荐行动，可多选，也可以只写自己的</p>
      <div class="flex flex-wrap gap-1.5">
        <button
          v-for="opt in options"
          :key="opt"
          type="button"
          class="rounded-sm border px-2.5 py-1 text-left text-sm transition disabled:cursor-not-allowed disabled:opacity-50"
          :class="draft.pick.includes(opt) ? 'border-seal bg-seal/10 text-seal' : 'border-paper-edge text-ink hover:bg-paper-deep'"
          :aria-pressed="draft.pick.includes(opt)"
          :disabled="locked"
          @click="toggleOption(room, draft, opt)"
        >
          <span v-if="draft.pick.includes(opt)" aria-hidden="true">✓ </span>{{ opt }}
        </button>
      </div>
    </div>

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
      class="w-full resize-none rounded-sm border border-paper-edge bg-paper-deep/60 px-3 py-2 text-sm leading-normal text-ink outline-none transition focus:border-seal disabled:opacity-60"
      @keydown.ctrl.enter.prevent="submit"
    />

    <div class="flex items-center gap-2">
      <button
        type="button"
        class="rounded-sm border border-paper-edge px-2 py-1 text-xs text-ink-soft transition hover:bg-paper-deep disabled:opacity-50"
        :disabled="locked"
        title="插入台词括号；选中文字时会直接包住选中内容"
        @click="quotes"
      >
        「」台词
      </button>
      <button
        v-if="room.me.submitted"
        type="button"
        class="rounded-sm border border-paper-edge px-2 py-1 text-xs text-ink-soft transition hover:bg-paper-deep"
        @click="cancelEdit(draft)"
      >
        取消修改
      </button>
      <span v-if="count > 0" class="ml-auto font-type text-[11px]" :class="count > CHAT_MAX_CHARS ? 'text-seal' : 'text-ink-faint'">
        {{ count }}/{{ CHAT_MAX_CHARS }}
      </span>
      <BaseButton :class="count > 0 ? '' : 'ml-auto'" :disabled="locked || !answer || count > CHAT_MAX_CHARS" @click="submit">
        {{ room.me.submitted ? '重新提交' : '提交' }}
      </BaseButton>
    </div>
  </section>

  <section v-else-if="view === 'done'" class="shrink-0 space-y-1 border-t border-paper-edge bg-ledger/5 px-3 py-2 text-sm">
    <div class="flex items-center justify-between gap-2">
      <span class="text-ledger">✓ 已提交你的行动</span>
      <button type="button" class="shrink-0 text-seal transition hover:underline" @click="edit">修改</button>
    </div>
    <p v-if="room.me.draft" class="line-clamp-2 whitespace-pre-wrap text-xs text-ink-soft">{{ room.me.draft }}</p>
  </section>
</template>
