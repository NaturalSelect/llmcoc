<script setup lang="ts">
import { Clock } from '@lucide/vue'
import { computed } from 'vue'
import type { CharacterCard } from '@/api/types'
import StampBadge from '@/components/base/StampBadge.vue'
import StatBar from '@/components/base/StatBar.vue'
import type { HudFlash } from '@/game/hud'
import { hudChips, statNum } from '@/game/hud'
import type { TimePoint } from '@/game/roomState'

// NOTE: 小屏专用的紧凑状态条；桌面端同样的信息在右侧栏里，由父级用 lg:hidden 隐藏本组件
const props = defineProps<{ card: CharacterCard; flash: HudFlash; time: TimePoint | null }>()
const chips = computed(() => hudChips(props.card))
</script>

<template>
  <div class="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1.5 border-b border-line bg-surface px-4 py-2">
    <span class="max-w-32 truncate text-sm font-medium text-fg">{{ card.name || '调查员' }}</span>
    <StatBar class="w-40" label="HP" tone="hp" :value="statNum(card, 'hp')" :max="statNum(card, 'max_hp')" :flash="flash.hp" />
    <StatBar class="w-40" label="SAN" tone="san" :value="statNum(card, 'san')" :max="statNum(card, 'max_san')" :flash="flash.san" />
    <div class="hidden w-40 sm:block">
      <StatBar label="MP" tone="mp" :value="statNum(card, 'mp')" :max="statNum(card, 'max_mp')" />
    </div>
    <StampBadge v-for="chip in chips" :key="chip.text" tone="seal" :title="chip.title || undefined">{{ chip.text }}</StampBadge>
    <span v-if="time" class="ml-auto inline-flex items-center gap-1.5 text-xs tabular-nums text-fg-muted" :title="time.elapsed || undefined">
      <Clock :size="16" :stroke-width="1.75" class="shrink-0" />
      {{ time.label }}
    </span>
  </div>
</template>
