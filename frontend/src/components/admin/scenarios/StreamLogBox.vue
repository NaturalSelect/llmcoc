<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from 'vue'
import BaseButton from '@/components/base/BaseButton.vue'

// NOTE: 生成/编译多处共用的流式日志框：新增一行自动滚到底；运行中显示脉冲提示，结束后才允许清空
// clearable=false 用于日志由外部保存、本地清空没有意义的场合（线上模组的编译日志在 store 里）
const props = withDefaults(defineProps<{ title: string; lines: string[]; running: boolean; clearable?: boolean }>(), { clearable: true })
const emit = defineEmits<{ clear: [] }>()

const box = ref<HTMLElement | null>(null)

async function toBottom() {
  await nextTick()
  if (box.value) box.value.scrollTop = box.value.scrollHeight
}

// NOTE: 不能只监听 lines.length：线上模组传进来的是滚动窗口（满 1000 行后每进一行就丢一行），长度不再变化但内容在变；
// 那边每次有新行都会换一个新数组，所以同时监听数组本身；其他调用方原地 push，靠长度变化触发
watch(() => [props.lines, props.lines.length], toBottom)
// 重新打开弹窗时日志已经在了，没有「新增」事件，需要挂载后定位到最新一行
onMounted(toBottom)
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
      <BaseButton v-if="clearable && !running && lines.length" variant="ghost" size="sm" @click="emit('clear')">清空日志</BaseButton>
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
