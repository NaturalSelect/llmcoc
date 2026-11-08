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

const preClass =
  'max-h-[65vh] overflow-auto rounded-sm border border-paper-edge bg-paper-deep/50 p-3 font-type text-[11px] leading-[1.55] whitespace-pre-wrap text-ink'
const emptyClass = 'rounded-sm border border-paper-edge bg-paper-deep/40 p-4 text-sm text-ink-faint'
</script>

<template>
  <BaseModal :open="open" title="LLM 生成记录" max-width="max-w-4xl" @close="emit('close')">
    <p class="mb-4 font-type text-xs text-ink-faint">{{ log?.scenario_name || '未命名模组' }}</p>
    <p v-if="loading" class="py-8 text-center text-sm text-ink-faint">加载中…</p>
    <div v-else>
      <TabBar v-model="tab" :tabs="tabs" class="mb-4" />
      <div v-if="tab === 'story'">
        <div v-if="!log?.has_story" :class="emptyClass">该模组没有保存故事原文，可能是改动前生成的旧模组。</div>
        <pre v-else :class="preClass">{{ log?.story_document || '' }}</pre>
      </div>
      <div v-else>
        <div v-if="!log?.has_log" :class="emptyClass">该模组没有 AI 生成对话记录，可能是手动创建或上传导入的模组。</div>
        <pre v-else :class="preClass">{{ log?.log_text || '' }}</pre>
      </div>
    </div>
    <div class="mt-4 flex justify-end">
      <BaseButton variant="ghost" @click="emit('close')">关闭</BaseButton>
    </div>
  </BaseModal>
</template>
