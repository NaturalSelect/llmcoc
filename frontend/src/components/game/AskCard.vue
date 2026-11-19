<script setup lang="ts">
import { Backpack, BookOpen, Check, Eye, Flame, Hand, Pencil, Quote, Sparkles } from '@lucide/vue'
import { computed, nextTick, ref } from 'vue'
import type { CharacterCard } from '@/api/types'
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
  characterMadnessLabel,
  draftAnswer,
  findBurnableFailedCheck,
  insertDialogueQuotes,
  isCharacterMad,
  quickCharacterItems,
  quickCharacterSkills,
  startEdit,
  toggleOption,
} from '@/game/ask'
import { statNum } from '@/game/hud'
import type { Room } from '@/game/roomState'

// NOTE: 卡片紧跟在点名消息下面时问题已经在上一条消息里，不再重复；放在消息流末尾兜底时才需要带上问题原文
const props = defineProps<{ room: Room; draft: AskDraft; card?: CharacterCard | null; showQuestion?: boolean }>()
const emit = defineEmits<{ submit: [text: string]; shown: [] }>()

const input = ref<HTMLTextAreaElement | null>(null)
const quickTab = ref<'' | 'skills' | 'items'>('')

const view = computed(() => askView(props.room, props.draft))
const locked = computed(() => askLocked(props.room))
const options = computed(() => askOptions(props.room))
const answer = computed(() => draftAnswer(props.room, props.draft))
const count = computed(() => askCharCount(answer.value))
const currentLuck = computed(() => statNum(props.card, 'luck'))
const burnable = computed(() => findBurnableFailedCheck(props.room, props.card?.name, currentLuck.value))
const inUnderlyingMadness = computed(() => isCharacterMad(props.card))
const madnessLabel = computed(() => characterMadnessLabel(props.card))
const madnessSymptom = computed(() => String((props.card as { madness_symptom?: unknown } | null | undefined)?.madness_symptom || ''))
const quickSkills = computed(() => quickCharacterSkills(props.card))
const quickItems = computed(() => quickCharacterItems(props.card))
const hasQuickRef = computed(() => quickSkills.value.length > 0 || quickItems.value.length > 0)

function submit() {
  if (locked.value || !answer.value || count.value > CHAT_MAX_CHARS) return
  quickTab.value = ''
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

async function appendText(text: string) {
  if (locked.value) return
  const cur = props.draft.input.trim()
  props.draft.input = cur ? `${cur}\n${text}` : text
  await nextTick()
  const el = input.value
  if (el) {
    el.focus()
    const pos = props.draft.input.length
    el.setSelectionRange(pos, pos)
  }
}

function prefillBurnLuck() {
  const b = burnable.value
  if (!b || !b.affordable || locked.value) return
  void appendText(`我决定燃烧 ${b.cost} 点幸运，将刚才的「${b.skill}」检定（骰值 ${b.roll}，目标 ${b.need}）改判为成功。`)
}

function prefillPushRoll() {
  const b = burnable.value
  if (!b || !b.pushable || locked.value) return
  void appendText(`我决定对刚才失败的「${b.skill}」检定孤注一掷：`)
}

function prefillRealityCheck() {
  if (!inUnderlyingMadness.value || locked.value) return
  void appendText('我深吸一口气，试图对照周围细节向 KP 请求一次现实检定，确认刚才感知到的一切是否为幻觉：')
}

function toggleQuick(tab: 'skills' | 'items') {
  quickTab.value = quickTab.value === tab ? '' : tab
}

function insertSkill(name: string) {
  void appendText(`我尝试运用「${name}」`)
  quickTab.value = ''
}

function insertItem(name: string) {
  void appendText(`我取出「${name}」`)
  quickTab.value = ''
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

    <div v-if="burnable" class="space-y-2 rounded-xl border border-line bg-surface-2 p-3">
      <div class="flex flex-wrap items-center justify-between gap-2 text-xs">
        <span class="font-medium text-fg">
          命运抉择：{{ burnable.skill }}检定失败（{{ burnable.roll }} / 目标 {{ burnable.need }}）
        </span>
        <span class="tabular-nums text-fg-subtle">当前幸运 {{ currentLuck }}</span>
      </div>
      <div class="flex flex-wrap gap-2">
        <BaseButton
          variant="secondary"
          size="sm"
          :disabled="locked || !burnable.affordable"
          :title="burnable.affordable ? '声明消耗幸运值改判为成功' : `当前幸运 ${currentLuck}，不足 ${burnable.cost} 点`"
          @click="prefillBurnLuck"
        >
          <Sparkles :size="14" :stroke-width="1.75" />
          燃烧 {{ burnable.cost }} 点幸运改判成功
        </BaseButton>
        <BaseButton
          v-if="burnable.pushable"
          variant="ghost"
          size="sm"
          :disabled="locked"
          title="付出额外代价或换一种方式重掷，失败后果将更严重"
          @click="prefillPushRoll"
        >
          <Flame :size="14" :stroke-width="1.75" />
          孤注一掷
        </BaseButton>
      </div>
    </div>

    <div
      v-if="inUnderlyingMadness"
      class="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-san/35 bg-san/10 px-3 py-2 text-xs"
      :title="madnessSymptom ? `当前症状：${madnessSymptom}` : undefined"
    >
      <div class="min-w-0 flex-1 text-fg-muted">
        <span class="font-medium text-san">潜伏疯狂期（{{ madnessLabel }}）</span>
        <span class="ml-1.5 text-fg-subtle">若怀疑眼前所见非实，可主动要求现实检定</span>
      </div>
      <BaseButton variant="secondary" size="sm" :disabled="locked" @click="prefillRealityCheck">
        <Eye :size="14" :stroke-width="1.75" />
        请求现实检定
      </BaseButton>
    </div>

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

    <div v-if="quickTab === 'skills' && quickSkills.length" class="flex max-h-32 flex-wrap gap-1.5 overflow-y-auto rounded-lg border border-line bg-surface-2 p-2.5">
      <button
        v-for="sk in quickSkills"
        :key="sk.name"
        type="button"
        :disabled="locked"
        class="inline-flex items-center gap-1 rounded-md border border-line bg-surface px-2 py-1 text-xs text-fg transition hover:border-accent/60 hover:text-accent"
        @click="insertSkill(sk.name)"
      >
        <span>{{ sk.name }}</span>
        <span class="tabular-nums text-fg-subtle">{{ sk.value }}</span>
      </button>
    </div>

    <div v-if="quickTab === 'items' && quickItems.length" class="flex max-h-32 flex-wrap gap-1.5 overflow-y-auto rounded-lg border border-line bg-surface-2 p-2.5">
      <button
        v-for="it in quickItems"
        :key="it.name + ':' + it.desc"
        type="button"
        :disabled="locked"
        :title="it.desc || undefined"
        class="inline-flex items-center gap-1 rounded-md border border-line bg-surface px-2 py-1 text-xs text-fg transition hover:border-accent/60 hover:text-accent"
        @click="insertItem(it.name)"
      >
        <span>{{ it.name }}</span>
        <span v-if="it.count > 1" class="tabular-nums text-fg-subtle">x{{ it.count }}</span>
      </button>
    </div>

    <div class="flex flex-wrap items-center gap-2">
      <BaseButton variant="ghost" size="sm" :disabled="locked" title="插入台词括号；选中文字时会直接包住选中内容" @click="quotes">
        <Quote :size="16" :stroke-width="1.75" />
        「」台词
      </BaseButton>
      <BaseButton
        v-if="hasQuickRef && quickSkills.length"
        variant="ghost"
        size="sm"
        :disabled="locked"
        title="速查常用技能，点击填入行动"
        @click="toggleQuick('skills')"
      >
        <BookOpen :size="15" :stroke-width="1.75" />
        技能
      </BaseButton>
      <BaseButton
        v-if="hasQuickRef && quickItems.length"
        variant="ghost"
        size="sm"
        :disabled="locked"
        title="速查随身物品，点击填入行动"
        @click="toggleQuick('items')"
      >
        <Backpack :size="15" :stroke-width="1.75" />
        物品
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
