<script setup lang="ts">
import { computed } from 'vue'
import {
  askNames,
  diceOf,
  directorTiming,
  isEndedMessage,
  isMyMessage,
  messageError,
  msgKind,
  msgLabel,
  stripLegacyTags,
  timeOf,
  writerText,
} from '@/game/messages'
import type { Room, RoomMessage } from '@/game/roomState'
import { fmtDuration, fmtStepAvg, fmtTime } from '@/utils/format'

const props = defineProps<{ msg: RoomMessage; room: Room }>()
const emit = defineEmits<{ preview: [src: string] }>()

const kind = computed(() => msgKind(props.msg))
const mine = computed(() => isMyMessage(props.msg, props.room.userId))
const time = computed(() => timeOf(props.msg))
const timing = computed(() => directorTiming(props.msg))
const text = computed(() => writerText(props.room, props.msg))
const error = computed(() => messageError(props.msg))
const names = computed(() => askNames(props.msg))
const dice = computed(() => diceOf(props.msg))
const images = computed(() => (props.msg.status === 'done' ? props.msg.images || [] : []))

const frame = computed(() => {
  if (kind.value === 'player') return mine.value ? 'border-moss bg-moss/10' : 'border-moss/60 bg-paper-deep/40'
  if (kind.value === 'system') return 'border-ledger/60 bg-ledger/5'
  return 'border-seal/70 bg-paper'
})
const labelTone = computed(() => (kind.value === 'player' ? 'text-moss' : kind.value === 'system' ? 'text-ledger' : 'text-seal'))
</script>

<template>
  <article class="msg rounded-sm border border-l-4 border-paper-edge px-3 py-2 shadow-sm" :class="[frame, msg._new ? 'fade-up' : '']">
    <div class="mb-1.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 border-b border-paper-edge/70 pb-1 font-type text-xs">
      <span class="font-bold" :class="labelTone">{{ msgLabel(msg) }}</span>
      <span class="text-ink-faint">{{ fmtTime(msg.created_at as string | undefined) }}</span>
      <span
        v-if="timing"
        class="text-ink-faint"
        title="KP决策耗时(不含叙事生成),Director工具调用循环轮数及平均每步延迟"
      >
        ⏱ {{ fmtDuration(timing.elapsedMs) }} · {{ timing.steps }} 步 · 均 {{ fmtStepAvg(timing.elapsedMs, timing.steps) }}/步
      </span>
      <span v-if="time" class="ml-auto text-ink-soft" :title="time.elapsed || undefined">🕯 {{ time.label }}</span>
    </div>

    <p v-if="kind === 'player'" class="whitespace-pre-wrap text-sm leading-relaxed text-ink">{{ msg.content }}</p>

    <div v-else-if="kind === 'writer'">
      <p v-if="msg.status === 'failed'" class="text-sm text-seal/80">叙事生成失败<template v-if="error">：{{ error }}</template></p>
      <template v-else>
        <p v-if="text.trim()" class="narrative whitespace-pre-wrap leading-loose text-ink" :class="msg.status === 'pending' ? 'cursor' : ''">{{ text }}</p>
        <p v-else-if="msg.status === 'pending'" class="narrative cursor text-ink-faint">叙事正文生成中…</p>
      </template>
    </div>

    <div v-else-if="kind === 'image'">
      <p v-if="msg.status === 'pending'" class="narrative cursor text-ink-faint">配图生成中…</p>
      <p v-else-if="msg.status === 'failed'" class="text-sm text-seal/80">配图失败<template v-if="error">：{{ error }}</template></p>
      <div v-if="images.length" class="grid gap-2">
        <img
          v-for="(img, idx) in images"
          :key="idx"
          :src="img"
          alt="KP生成的场景图"
          loading="lazy"
          decoding="async"
          class="max-h-96 max-w-full cursor-zoom-in rounded-sm border border-paper-edge bg-ink/5 object-contain transition-opacity hover:opacity-90"
          @click="emit('preview', img)"
        />
      </div>
    </div>

    <div v-else-if="kind === 'kp' || kind === 'ask'">
      <p class="whitespace-pre-wrap leading-loose text-ink">{{ msg.content }}</p>
      <p v-if="kind === 'ask' && names.length" class="mt-1.5 font-type text-xs font-bold text-seal">👉 KP 点名：{{ names.join('、') }}</p>
      <p v-if="isEndedMessage(msg)" class="mt-1.5 font-type text-xs font-bold text-seal">— 游戏结束 —</p>
      <div v-if="dice.length" class="mt-2 flex flex-wrap gap-1.5">
        <span
          v-for="(d, idx) in dice"
          :key="idx"
          class="inline-flex items-center gap-1.5 rounded-sm border border-paper-edge bg-paper-deep/60 px-2 py-0.5 font-type text-xs text-ink"
        >
          <template v-if="'raw' in d">{{ d.raw }}</template>
          <template v-else>
            <span :title="d.what || undefined">🎲 {{ d.what || '骰点' }}{{ d.hidden ? '（暗骰）' : '' }}</span>
            <b class="text-seal">{{ d.roll }}</b>
            <span v-if="d.level" class="text-ink-faint">难度 {{ d.level }}</span>
          </template>
        </span>
      </div>
    </div>

    <p v-else-if="kind === 'system'" class="whitespace-pre-wrap text-xs italic text-ledger">{{ msg.content }}</p>

    <p v-else class="narrative whitespace-pre-wrap leading-loose text-ink">{{ stripLegacyTags(msg.content) }}</p>
  </article>
</template>

<style scoped>
.cursor::after {
  content: '▍';
  margin-left: 1px;
  animation: blink 1s steps(2, start) infinite;
}
.fade-up {
  animation: fadeUp 0.3s ease-out;
}
@keyframes blink {
  to {
    visibility: hidden;
  }
}
@keyframes fadeUp {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
}
@media (prefers-reduced-motion: reduce) {
  .cursor::after,
  .fade-up {
    animation: none;
  }
}
</style>
