<script setup lang="ts">
import { computed, reactive } from 'vue'
import type { ClassifierLog } from '@/api/classifierTypes'
import ClassifierChip from './ClassifierChip.vue'
import ClassifierDetail from './ClassifierDetail.vue'

// NOTE: 挂在玩家气泡下面的一排 chip（管理员专用）：chip 的宽度不超过气泡的上限、对齐方式和气泡一致；点开的详情用整行宽度，不会把气泡撑歪
const props = defineProps<{ logs: readonly ClassifierLog[]; end?: boolean }>()

const expanded = reactive(new Set<number>())
const opened = computed(() => props.logs.filter((l) => expanded.has(l.id)))

function toggle(id: number) {
  if (expanded.has(id)) expanded.delete(id)
  else expanded.add(id)
}
</script>

<template>
  <div class="mt-1.5 space-y-1.5" role="group" aria-label="分类器结果" data-testid="classifier-chips">
    <div class="flex w-fit max-w-full flex-wrap gap-1.5 sm:max-w-[85%]" :class="end ? 'ml-auto justify-end' : ''">
      <ClassifierChip
        v-for="log in logs"
        :key="log.id"
        :log="log"
        :expanded="expanded.has(log.id)"
        :controls="`classifier-detail-${log.id}`"
        @toggle="toggle(log.id)"
      />
    </div>
    <ClassifierDetail v-for="log in opened" :id="`classifier-detail-${log.id}`" :key="log.id" :log="log" />
  </div>
</template>
