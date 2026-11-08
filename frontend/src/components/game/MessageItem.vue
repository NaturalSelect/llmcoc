<script setup lang="ts">
import {
  Activity,
  BookOpen,
  Brain,
  Clock,
  Coins,
  Dices,
  EyeOff,
  Footprints,
  Hand,
  HeartPulse,
  MapPin,
  Package,
  Shield,
  Skull,
  Sparkles,
  Swords,
  Timer,
  Users,
} from '@lucide/vue'
import { computed } from 'vue'
import type { Component } from 'vue'
import { describeEvent, eventsOf } from '@/game/events'
import type { EventIcon, EventTone } from '@/game/events'
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
const events = computed(() => (kind.value === 'event' ? eventsOf(props.msg).map(describeEvent) : []))

const EVENT_ICONS: Record<EventIcon, Component> = {
  dice: Dices,
  hidden: EyeOff,
  hp: HeartPulse,
  san: Brain,
  mp: Sparkles,
  stat: Activity,
  armor: Shield,
  wound: Skull,
  item: Package,
  spell: BookOpen,
  monster: Skull,
  relation: Users,
  asset: Coins,
  madness: Brain,
  location: MapPin,
  time: Clock,
  combat: Swords,
  chase: Footprints,
}

const EVENT_TONES: Record<EventTone, string> = {
  plain: 'text-fg-subtle',
  hp: 'text-hp',
  san: 'text-san',
  mp: 'text-mp',
  danger: 'text-danger',
}
</script>

<template>
  <article :class="msg._new ? 'fade-up' : ''">
    <!-- NOTE: 叙事类消息（writer / kp / ask / 旧数据）是无框的连续正文；系统消息居中且两侧用发丝线，不抢叙事的视线 -->
    <div v-if="kind === 'system'" class="flex items-center gap-3 text-xs text-fg-subtle">
      <span class="h-px flex-1 bg-line" aria-hidden="true" />
      <p class="max-w-[85%] whitespace-pre-wrap text-center">{{ msg.content }}</p>
      <span class="h-px flex-1 bg-line" aria-hidden="true" />
    </div>

    <!-- 玩家发言：安静的气泡，不用彩色边框 -->
    <div v-else-if="kind === 'player'" class="w-fit max-w-full rounded-xl bg-surface-2 px-4 py-3 sm:max-w-[85%]" :class="mine ? 'ml-auto' : ''">
      <div class="mb-1 flex flex-wrap items-baseline gap-x-2">
        <span class="text-sm font-medium text-accent">{{ msgLabel(msg) }}</span>
        <span class="text-xs tabular-nums text-fg-subtle">{{ fmtTime(msg.created_at as string | undefined) }}</span>
        <span v-if="time" class="ml-auto inline-flex items-center gap-1 text-xs tabular-nums text-fg-subtle" :title="time.elapsed || undefined">
          <Clock :size="16" :stroke-width="1.75" class="shrink-0" />
          {{ time.label }}
        </span>
      </div>
      <p class="whitespace-pre-wrap text-sm leading-relaxed text-fg">{{ msg.content }}</p>
    </div>

    <!-- KP 工具结算：紧凑的一行一条，不带表头，不判成败(成败由 KP 在叙事里说) -->
    <ul v-else-if="kind === 'event'" class="space-y-1 text-xs text-fg-muted" aria-label="KP 结算">
      <li v-for="(ev, idx) in events" :key="idx" class="flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
        <component :is="EVENT_ICONS[ev.icon]" :size="16" :stroke-width="1.75" class="shrink-0" :class="EVENT_TONES[ev.tone]" />
        <span :class="ev.tone === 'danger' ? 'text-danger' : ''">{{ ev.text }}</span>
        <b v-if="ev.value" class="font-semibold tabular-nums" :class="ev.tone === 'plain' ? 'text-accent' : EVENT_TONES[ev.tone]">{{ ev.value }}</b>
        <span v-if="ev.note" class="text-fg-subtle">{{ ev.note }}</span>
      </li>
    </ul>

    <template v-else>
      <div class="mb-1.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-fg-subtle">
        <span class="font-medium">{{ msgLabel(msg) }}</span>
        <span class="tabular-nums">{{ fmtTime(msg.created_at as string | undefined) }}</span>
        <span
          v-if="timing"
          class="inline-flex items-center gap-1 tabular-nums"
          title="KP决策耗时(不含叙事生成),Director工具调用循环轮数及平均每步延迟"
        >
          <Timer :size="16" :stroke-width="1.75" class="shrink-0" />
          {{ fmtDuration(timing.elapsedMs) }} · {{ timing.steps }} 步 · 均 {{ fmtStepAvg(timing.elapsedMs, timing.steps) }}/步
        </span>
        <span v-if="time" class="ml-auto inline-flex items-center gap-1 tabular-nums" :title="time.elapsed || undefined">
          <Clock :size="16" :stroke-width="1.75" class="shrink-0" />
          {{ time.label }}
        </span>
      </div>

      <div v-if="kind === 'writer'">
        <p v-if="msg.status === 'failed'" class="text-sm text-danger">叙事生成失败<template v-if="error">：{{ error }}</template></p>
        <template v-else>
          <p v-if="text.trim()" class="whitespace-pre-wrap font-prose text-prose text-fg" :class="msg.status === 'pending' ? 'cursor' : ''">{{ text }}</p>
          <p v-else-if="msg.status === 'pending'" class="cursor font-prose text-prose text-fg-subtle">叙事正文生成中…</p>
        </template>
      </div>

      <div v-else-if="kind === 'image'">
        <p v-if="msg.status === 'pending'" class="cursor text-sm text-fg-subtle">配图生成中…</p>
        <p v-else-if="msg.status === 'failed'" class="text-sm text-danger">配图失败<template v-if="error">：{{ error }}</template></p>
        <div v-if="images.length" class="grid gap-3">
          <img
            v-for="(img, idx) in images"
            :key="idx"
            :src="img"
            alt="KP生成的场景图"
            loading="lazy"
            decoding="async"
            class="w-full max-w-full cursor-zoom-in rounded-xl bg-surface-2 object-contain transition-opacity hover:opacity-90"
            @click="emit('preview', img)"
          />
        </div>
      </div>

      <div v-else-if="kind === 'kp' || kind === 'ask'">
        <p class="whitespace-pre-wrap font-prose text-prose text-fg">{{ msg.content }}</p>
        <p
          v-if="kind === 'ask' && names.length"
          class="mt-3 inline-flex items-center gap-1.5 rounded-full bg-accent/15 px-2.5 py-1 text-xs font-medium text-accent"
        >
          <Hand :size="16" :stroke-width="1.75" class="shrink-0" />
          点名：{{ names.join('、') }}
        </p>
        <p v-if="isEndedMessage(msg)" class="mt-3 text-sm font-medium text-accent">— 游戏结束 —</p>
        <div v-if="dice.length" class="mt-3 flex flex-wrap gap-2">
          <span
            v-for="(d, idx) in dice"
            :key="idx"
            class="inline-flex items-center gap-1.5 rounded-full bg-surface-3 px-2.5 py-1 text-xs tabular-nums text-fg"
          >
            <Dices :size="16" :stroke-width="1.75" class="shrink-0 text-fg-subtle" />
            <template v-if="'raw' in d">{{ d.raw }}</template>
            <template v-else>
              <span :title="d.what || undefined">{{ d.what || '骰点' }}{{ d.hidden ? '（暗骰）' : '' }}</span>
              <b class="font-semibold text-accent">{{ d.roll }}</b>
              <span v-if="d.level" class="text-fg-subtle">难度 {{ d.level }}</span>
            </template>
          </span>
        </div>
      </div>

      <p v-else class="whitespace-pre-wrap font-prose text-prose text-fg">{{ stripLegacyTags(msg.content) }}</p>
    </template>
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
