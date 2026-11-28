<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ClassifierLog } from '@/api/classifierTypes'
import { fmtClock, inputSummary, orphanNote } from '@/game/classifier'
import ClassifierChip from './ClassifierChip.vue'
import ClassifierDetail from './ClassifierDetail.vue'

// NOTE: 管理员专用：还没成为任何玩家消息的输入分类记录（被拦截、被后一次提交取代、还在等其他玩家）。
// 风格参考系统消息：两侧发丝线夹一行低调的摘要，点 chip 展开同样的详情；输入内容只做文本插值
const props = defineProps<{ log: ClassifierLog }>()

const expanded = ref(false)
const input = computed(() => inputSummary(props.log))
const note = computed(() => orphanNote(props.log))
</script>

<template>
  <div class="text-xs text-fg-subtle" data-testid="classifier-orphan">
    <div class="flex items-center gap-3">
      <span class="h-px flex-1 bg-line" aria-hidden="true" />
      <div class="flex max-w-[85%] min-w-0 items-center gap-2">
        <span class="hidden shrink-0 tabular-nums sm:inline" data-testid="orphan-time">{{ fmtClock(log.created_at) }}</span>
        <span class="shrink-0 font-medium text-fg-muted" data-testid="orphan-subject">{{ log.subject || '未知调查员' }}</span>
        <span v-if="input" class="min-w-0 truncate" :title="input" data-testid="orphan-input">「{{ input }}」</span>
        <ClassifierChip :log="log" :expanded="expanded" :controls="`classifier-detail-${log.id}`" @toggle="expanded = !expanded" />
      </div>
      <span class="h-px flex-1 bg-line" aria-hidden="true" />
    </div>
    <p v-if="note" class="mt-1 text-center" data-testid="orphan-note">{{ note }}</p>
    <ClassifierDetail v-if="expanded" :id="`classifier-detail-${log.id}`" class="mt-2" :log="log" />
  </div>
</template>
