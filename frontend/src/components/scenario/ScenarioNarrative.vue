<script setup lang="ts">
import type { ScenarioContentView } from '@/api/sessionTypes'
import ScenarioSection from './ScenarioSection.vue'

// NOTE: 四段纯文本全部走文本插值，不经过 v-html（模组内容含 AI 生成文字，不可信）
defineProps<{ content: ScenarioContentView }>()
</script>

<template>
  <ScenarioSection v-if="content.setting" title="背景设定">
    <p class="whitespace-pre-wrap font-prose text-prose text-fg">{{ content.setting }}</p>
  </ScenarioSection>

  <ScenarioSection v-if="content.intro" title="开场叙事">
    <p class="whitespace-pre-wrap font-prose text-prose italic text-fg-muted">{{ content.intro }}</p>
  </ScenarioSection>

  <ScenarioSection v-if="content.map_description" title="地图说明">
    <div class="whitespace-pre-wrap rounded-lg bg-surface-2 p-3 text-sm leading-relaxed text-fg">
      {{ content.map_description }}
    </div>
  </ScenarioSection>

  <ScenarioSection v-if="content.playthrough_outline" title="游玩大纲">
    <div class="whitespace-pre-wrap rounded-lg bg-surface-2 p-3 text-sm leading-relaxed text-fg">
      {{ content.playthrough_outline }}
    </div>
  </ScenarioSection>
</template>
