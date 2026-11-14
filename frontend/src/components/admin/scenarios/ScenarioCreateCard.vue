<script setup lang="ts">
import { ref } from 'vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import TabBar from '@/components/base/TabBar.vue'
import ScenarioGeneratePanel from './ScenarioGeneratePanel.vue'
import ScenarioImportPanel from './ScenarioImportPanel.vue'
import StoryCompilePanel from './StoryCompilePanel.vue'

defineProps<{ reload: () => Promise<void> }>()

const TABS = [
  { key: 'generate', label: 'AI 生成' },
  { key: 'compile', label: '故事编译' },
  { key: 'import', label: '导入 JSON' },
]
const tab = ref('generate')
</script>

<template>
  <PaperCard>
    <SectionHeading>新建模组</SectionHeading>
    <TabBar v-model="tab" :tabs="TABS" class="mt-2 mb-4" />
    <!-- NOTE: 必须用 v-show：生成和编译面板卸载时会中断在途的 WebSocket，切页签不能让进度日志丢失 -->
    <ScenarioGeneratePanel v-show="tab === 'generate'" :reload="reload" />
    <StoryCompilePanel v-show="tab === 'compile'" :reload="reload" />
    <ScenarioImportPanel v-show="tab === 'import'" :reload="reload" />
  </PaperCard>
</template>
