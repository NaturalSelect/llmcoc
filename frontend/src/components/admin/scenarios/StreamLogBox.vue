<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'

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
  <div class="mt-3">
    <div class="mb-1 flex items-center gap-2">
      <span class="font-type text-xs text-ink-soft">{{ title }}</span>
      <span v-if="running" class="animate-pulse text-[11px] text-seal">● 实时流式输出中</span>
    </div>
    <div
      ref="box"
      role="log"
      class="max-h-[260px] overflow-y-auto rounded-sm border border-paper-edge bg-paper-deep/60 p-3 font-type text-[11px] leading-relaxed break-all whitespace-pre-wrap text-ink-soft"
    >
      <div v-for="(line, idx) in lines" :key="idx">{{ line }}</div>
    </div>
    <button
      v-if="!running && lines.length"
      type="button"
      class="mt-1 text-[11px] text-ink-soft underline transition hover:text-seal"
      @click="emit('clear')"
    >
      清空日志
    </button>
  </div>
</template>
