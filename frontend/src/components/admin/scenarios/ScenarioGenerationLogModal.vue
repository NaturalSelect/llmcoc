<script setup lang="ts">
import { ref, watch } from 'vue'
import type { ScenarioGenerationLog } from '@/api/adminTypesContent'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import TabBar from '@/components/base/TabBar.vue'

const props = defineProps<{ open: boolean; loading: boolean; log: ScenarioGenerationLog | null }>()
const emit = defineEmits<{ close: [] }>()

const tab = ref('story')
const tabs = [
  { key: 'story', label: '故事原文' },
  { key: 'dialog', label: 'LLM对话日志' },
]

// NOTE: 弹窗常驻挂载，每次打开都要回到「故事原文」，与旧实现每次 viewScenarioGenerationLog 重置一致
watch(
  () => props.open,
  (open) => {
    if (open) tab.value = 'story'
  },
)

// NOTE: 故事原文是正文，用 prose 字体；LLM 对话记录是日志，用等宽小字
const storyClass =
  'overflow-x-auto rounded-lg bg-bg p-4 font-prose text-sm leading-relaxed whitespace-pre-wrap text-fg'
const logClass = 'overflow-x-auto rounded-lg bg-bg p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-fg-muted'
const emptyClass = 'rounded-lg bg-surface-2 p-4 text-sm text-fg-muted'
</script>

<template>
  <BaseModal :open="open" title="LLM 生成记录" max-width="max-w-4xl" @close="emit('close')">
    <p class="mb-4 text-sm text-fg-muted">{{ log?.scenario_name || '未命名模组' }}</p>
    <p v-if="loading" class="py-8 text-center text-sm text-fg-subtle">加载中…</p>
    <div v-else>
      <TabBar v-model="tab" :tabs="tabs" class="mb-4" />
      <div v-if="tab === 'story'">
        <div v-if="!log?.has_story" :class="emptyClass">该模组没有保存故事原文，可能是改动前生成的旧模组。</div>
        <pre v-else :class="storyClass">{{ log?.story_document || '' }}</pre>
      </div>
      <div v-else>
        <div v-if="!log?.has_log" :class="emptyClass">该模组没有 AI 生成对话记录，可能是手动创建或上传导入的模组。</div>
        <pre v-else :class="logClass">{{ log?.log_text || '' }}</pre>
      </div>
    </div>
    <div class="mt-4 flex justify-end">
      <BaseButton variant="ghost" @click="emit('close')">关闭</BaseButton>
    </div>
  </BaseModal>
</template>
