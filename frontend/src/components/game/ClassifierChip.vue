<script setup lang="ts">
import { ChevronDown, ChevronRight } from '@lucide/vue'
import type { ClassifierLog } from '@/api/classifierTypes'
import StampBadge from '@/components/base/StampBadge.vue'
import { chipDecisionLabel, decisionTone, fmtScore, kindLabel } from '@/game/classifier'

// NOTE: 一条分类器记录的紧凑摘要，是个展开按钮：「输入分类 · 放行 0.12 / 0.80」。展开状态由父组件管，详情渲染在父组件里（宽度要用整行）
defineProps<{ log: ClassifierLog; expanded: boolean; controls: string }>()
defineEmits<{ toggle: [] }>()
</script>

<template>
  <button
    type="button"
    class="inline-flex min-w-0 max-w-full shrink-0 items-center gap-1.5 rounded-full border border-line bg-surface px-2 py-0.5 text-xs text-fg-muted transition hover:bg-surface-3 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
    :aria-expanded="expanded"
    :aria-controls="controls"
    data-testid="classifier-chip"
    @click="$emit('toggle')"
  >
    <component :is="expanded ? ChevronDown : ChevronRight" :size="14" :stroke-width="1.75" class="shrink-0 text-fg-subtle" />
    <span class="shrink-0" data-testid="chip-kind">{{ kindLabel(log.kind) }}</span>
    <span class="shrink-0 text-fg-subtle" aria-hidden="true">·</span>
    <StampBadge class="shrink-0" :tone="decisionTone(log)" data-testid="chip-decision">{{ chipDecisionLabel(log) }}</StampBadge>
    <span class="shrink-0 tabular-nums" data-testid="chip-score">{{ fmtScore(log) }}</span>
  </button>
</template>
