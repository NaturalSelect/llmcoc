<script setup lang="ts">
import { ref, watch } from 'vue'
import BaseModal from '@/components/base/BaseModal.vue'
import TabBar from '@/components/base/TabBar.vue'
import AiCreatePanel from './AiCreatePanel.vue'
import ManualCreatePanel from './ManualCreatePanel.vue'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const TABS = [
  { key: 'manual', label: '规则车卡' },
  { key: 'ai', label: 'AI生成' },
]

const mode = ref('ai')

// NOTE: 面板在 BaseModal 的 v-if 里，每次打开都会重新挂载（表单、草稿全部重置，与旧前端一致）；
// 两个面板之间用 v-show 切换，掷骰得到的草稿不会因为切到 AI 标签而丢失
watch(
  () => props.open,
  (open) => {
    if (open) mode.value = 'ai'
  },
)
</script>

<template>
  <BaseModal :open="open" title="创建调查员" max-width="max-w-4xl" @close="emit('close')">
    <TabBar v-model="mode" :tabs="TABS" class="mb-4" />
    <ManualCreatePanel v-show="mode === 'manual'" :active="mode === 'manual'" @close="emit('close')" />
    <AiCreatePanel v-show="mode === 'ai'" @close="emit('close')" />
  </BaseModal>
</template>
