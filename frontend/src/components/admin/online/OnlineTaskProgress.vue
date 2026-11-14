<script setup lang="ts">
import { LoaderCircle } from '@lucide/vue'
import { computed } from 'vue'
import type { OnlineModule } from '@/api/adminTypesOnline'
import { formatElapsed, stageLabel, taskElapsedMs } from './onlineModules'

// NOTE: now 由调用方统一提供（页面只开一个定时器），这里不自己计时
const props = defineProps<{ module: Pick<OnlineModule, 'stage' | 'stage_note' | 'task_started_at'>; now: number }>()

const elapsed = computed(() => taskElapsedMs(props.module, props.now))
</script>

<template>
  <p class="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs tabular-nums text-fg-muted" role="status" data-testid="task-progress">
    <LoaderCircle :size="14" :stroke-width="1.75" class="shrink-0 animate-spin text-info" />
    <span>{{ stageLabel(module.stage) || '处理中' }}</span>
    <span v-if="module.stage_note" class="break-all">· {{ module.stage_note }}</span>
    <span v-if="elapsed !== null">· 已用时 {{ formatElapsed(elapsed) }}</span>
  </p>
</template>
