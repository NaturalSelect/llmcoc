<script setup lang="ts">
import { computed } from 'vue'
import { continueLabel, roomBanner } from '@/game/banner'
import * as RS from '@/game/roomState'
import type { Room } from '@/game/roomState'

const props = defineProps<{ room: Room }>()
const emit = defineEmits<{ continue: []; stop: [] }>()

const banner = computed(() => roomBanner(props.room))
const gates = computed(() => RS.gates(props.room))

const toneClass = computed(
  () =>
    ({
      warn: 'text-seal',
      me: 'font-semibold text-seal',
      info: 'text-ledger',
      idle: 'text-ink-soft',
    })[banner.value.tone],
)
</script>

<template>
  <div
    class="flex shrink-0 items-center gap-2 border-t border-paper-edge bg-paper-deep px-3 py-2"
    :data-tone="banner.tone"
    :data-busy="banner.busy ? 'true' : 'false'"
    role="status"
    aria-live="polite"
  >
    <span class="size-2 shrink-0 rounded-full bg-current" :class="[toneClass, banner.busy ? 'dot-pulse' : '']" aria-hidden="true" />
    <span class="line-clamp-2 min-w-0 flex-1 text-sm" :class="toneClass">{{ banner.text }}</span>
    <div class="flex shrink-0 items-center gap-1.5">
      <button
        v-if="gates.canContinue"
        type="button"
        class="rounded-sm border border-seal bg-seal px-3 py-1 text-xs text-paper transition hover:bg-seal/90"
        title="跳过未回应的玩家，让 KP 继续推进剧情"
        @click="emit('continue')"
      >
        {{ continueLabel(room) }}
      </button>
      <button
        v-if="gates.canStop"
        type="button"
        class="rounded-sm border border-seal px-3 py-1 text-xs text-seal transition hover:bg-seal/10"
        title="打断 KP 当前的发言，本周期已发布的内容会保留"
        @click="emit('stop')"
      >
        打断 KP
      </button>
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
