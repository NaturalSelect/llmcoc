<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import BaseButton from '@/components/base/BaseButton.vue'

// NOTE: 生成/编译两处共用的流式日志框：新增一行自动滚到底；运行中显示脉冲提示，结束后才允许清空
const props = defineProps<{ title: string; lines: string[]; running: boolean }>()
const emit = defineEmits<{ clear: [] }>()

const box = ref<HTMLElement | null>(null)

watch(
  () => props.lines.length,
  async () => {
    await nextTick()
    if (box.value) box.value.scrollTop = box.value.scrollHeight
  },
)
</script>

<template>
  <div class="mt-4">
    <div class="mb-2 flex items-center justify-between gap-2">
      <div class="flex items-center gap-3">
        <span class="text-sm text-fg-muted">{{ title }}</span>
        <span v-if="running" class="inline-flex items-center gap-1.5 text-xs text-accent">
          <span class="size-1.5 animate-pulse rounded-full bg-accent" aria-hidden="true" />
          实时流式输出中
        </span>
      </div>
      <BaseButton v-if="!running && lines.length" variant="ghost" size="sm" @click="emit('clear')">清空日志</BaseButton>
    </div>
    <div
      ref="box"
      role="log"
      class="max-h-64 overflow-y-auto rounded-lg bg-bg p-3 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap text-fg-muted"
    >
      <div v-for="(line, idx) in lines" :key="idx">{{ line }}</div>
    </div>
  </div>
</template>
