<script setup lang="ts">
import type { ScenarioContentView } from '@/api/sessionTypes'
import ScenarioSection from './ScenarioSection.vue'

// NOTE: 四段纯文本全部走文本插值，不经过 v-html（模组内容含 AI 生成文字，不可信）
defineProps<{ content: ScenarioContentView }>()
</script>

<template>
  <ScenarioSection v-if="content.setting" title="背景设定">
    <p class="pl-3 font-serif text-sm leading-[1.7] text-ink">{{ content.setting }}</p>
  </ScenarioSection>

  <ScenarioSection v-if="content.intro" title="开场叙事">
    <p class="pl-3 font-serif text-sm italic leading-[1.7] text-ink-soft">{{ content.intro }}</p>
  </ScenarioSection>

  <ScenarioSection v-if="content.map_description" title="地图说明">
    <div class="whitespace-pre-wrap rounded-sm border border-paper-edge bg-paper-deep/50 p-2.5 font-serif text-sm leading-relaxed text-ink">
      {{ content.map_description }}
    </div>
  </ScenarioSection>

  <ScenarioSection v-if="content.playthrough_outline" title="游玩大纲">
    <div class="whitespace-pre-wrap rounded-sm border border-paper-edge bg-paper-deep/50 p-2.5 font-serif text-sm leading-relaxed text-ink">
      {{ content.playthrough_outline }}
    </div>
  </ScenarioSection>
</template>
