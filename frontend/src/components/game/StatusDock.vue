<script setup lang="ts">
import { FastForward, OctagonX } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import BaseButton from '@/components/base/BaseButton.vue'
import { continueLabel, roomBanner, waitedLabel } from '@/game/banner'
import * as RS from '@/game/roomState'
import type { Room } from '@/game/roomState'

const props = defineProps<{ room: Room }>()
const emit = defineEmits<{ continue: []; stop: [] }>()

const banner = computed(() => roomBanner(props.room))
const gates = computed(() => RS.gates(props.room))

// NOTE: 计时器只在周期运行时走，状态栏隐藏或周期结束后立即停掉
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | null = null
function stopTimer() {
  if (timer) clearInterval(timer)
  timer = null
}
watch(
  () => props.room.floor.running,
  (running) => {
    stopTimer()
    if (!running) return
    now.value = Date.now()
    timer = setInterval(() => (now.value = Date.now()), 1000)
  },
  { immediate: true },
)
onBeforeUnmount(stopTimer)
const waited = computed(() => waitedLabel(props.room, now.value))

const toneClass = computed(
  () =>
    ({
      warn: 'text-warning',
      me: 'font-medium text-accent',
      info: 'text-info',
      idle: 'text-fg-muted',
    })[banner.value.tone],
)
</script>

<template>
  <div
    class="flex items-center gap-3 rounded-xl bg-surface-2 px-4 py-2.5"
    :data-tone="banner.tone"
    :data-busy="banner.busy ? 'true' : 'false'"
    role="status"
    aria-live="polite"
  >
    <span class="size-2 shrink-0 rounded-full bg-current" :class="[toneClass, banner.busy ? 'dot-pulse' : '']" aria-hidden="true" />
    <span class="line-clamp-2 min-w-0 flex-1 text-sm" :class="toneClass">{{ banner.text }}</span>
    <span v-if="waited" class="shrink-0 text-xs tabular-nums text-fg-subtle">{{ waited }}</span>
    <div class="flex shrink-0 items-center gap-2">
      <BaseButton v-if="gates.canContinue" size="sm" title="跳过未回应的玩家，让 KP 继续推进剧情" @click="emit('continue')">
        <FastForward :size="16" :stroke-width="1.75" />
        {{ continueLabel(room) }}
      </BaseButton>
      <BaseButton v-if="gates.canStop" variant="danger" size="sm" title="打断 KP 当前的发言，本周期已发布的内容会保留" @click="emit('stop')">
        <OctagonX :size="16" :stroke-width="1.75" />
        打断 KP
      </BaseButton>
    </div>
  </div>
</template>

<style scoped>
.dot-pulse {
  animation: dockPulse 1.4s ease-in-out infinite;
}
@keyframes dockPulse {
  50% {
    opacity: 0.35;
  }
}
@media (prefers-reduced-motion: reduce) {
  .dot-pulse {
    animation: none;
  }
}
</style>
