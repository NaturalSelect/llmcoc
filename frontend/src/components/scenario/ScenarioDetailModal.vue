<script setup lang="ts">
import { computed } from 'vue'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import type { Scenario } from '@/api/scenarioTypes'
import ScenarioClues from './ScenarioClues.vue'
import ScenarioEndings from './ScenarioEndings.vue'
import ScenarioHeaderMeta from './ScenarioHeaderMeta.vue'
import ScenarioKeeperAppendix from './ScenarioKeeperAppendix.vue'
import ScenarioMechanics from './ScenarioMechanics.vue'
import ScenarioMythosCore from './ScenarioMythosCore.vue'
import ScenarioNarrative from './ScenarioNarrative.vue'
import ScenarioNpcs from './ScenarioNpcs.vue'
import ScenarioReward from './ScenarioReward.vue'
import ScenarioScenes from './ScenarioScenes.vue'
import ScenarioStatsBar from './ScenarioStatsBar.vue'
import ScenarioTimeline from './ScenarioTimeline.vue'
import { scenarioContent } from './scenarioFormat'

// NOTE: 大厅选剧本弹窗和后台模组页共用，props/emits 是对外契约，不要改
const props = defineProps<{ open: boolean; scenario: Scenario | null }>()
const emit = defineEmits<{ close: [] }>()

const content = computed(() => scenarioContent(props.scenario))
</script>

<template>
  <BaseModal :open="open" :title="scenario?.name" max-width="max-w-3xl" @close="emit('close')">
    <template v-if="scenario">
      <ScenarioHeaderMeta :scenario="scenario" />
      <ScenarioStatsBar :content="content" />
      <ScenarioNarrative :content="content" />
      <ScenarioTimeline v-if="content.timeline?.length" :timeline="content.timeline" />
      <ScenarioEndings v-if="content.endings?.length" :endings="content.endings" />
      <ScenarioReward v-if="content.reward" :reward="content.reward" />
      <ScenarioScenes v-if="content.scenes?.length" :scenes="content.scenes" />
      <ScenarioNpcs v-if="content.npcs?.length" :npcs="content.npcs" />
      <ScenarioMythosCore v-if="content.mythos_core" :text="content.mythos_core" />
      <ScenarioClues v-if="content.clues?.length" :clues="content.clues" />
      <ScenarioKeeperAppendix v-if="content.keeper_appendix" :appendix="content.keeper_appendix" />
      <ScenarioMechanics v-if="content.mechanics?.length" :mechanics="content.mechanics" />
    </template>
    <div class="mt-4 flex justify-end">
      <BaseButton variant="ghost" @click="emit('close')">关闭</BaseButton>
    </div>
  </BaseModal>
</template>
