<script setup lang="ts">
import type { ScenarioNpc } from '@/api/sessionTypes'
import ScenarioChip from './ScenarioChip.vue'
import ScenarioSection from './ScenarioSection.vue'
import { attitudeTone, countLabel, entriesOf, hasMythosSkill } from './scenarioFormat'

defineProps<{ npcs: ScenarioNpc[] }>()
</script>

<template>
  <ScenarioSection title="NPC 列表" :note="countLabel(npcs.length, '人')">
    <ul class="space-y-2">
      <li v-for="(npc, i) in npcs" :key="`${npc.name ?? ''}-${i}`" class="rounded-lg bg-surface-2 p-3">
        <div class="mb-1.5 flex items-start justify-between gap-2">
          <span class="text-sm font-semibold text-fg">{{ npc.name }}</span>
          <ScenarioChip v-if="npc.attitude" :tone="attitudeTone(npc.attitude)" class="shrink-0">{{ npc.attitude }}</ScenarioChip>
        </div>
        <div v-if="npc.race || npc.occupation || hasMythosSkill(npc.cthulhu_mythos_skill)" class="mb-2 flex flex-wrap gap-1.5">
          <ScenarioChip v-if="npc.race">种族: {{ npc.race }}</ScenarioChip>
          <ScenarioChip v-if="npc.occupation">职业: {{ npc.occupation }}</ScenarioChip>
          <ScenarioChip v-if="hasMythosSkill(npc.cthulhu_mythos_skill)">克苏鲁神话: {{ npc.cthulhu_mythos_skill }}</ScenarioChip>
        </div>
        <p v-if="npc.description" class="mb-2 text-sm leading-relaxed text-fg-muted">{{ npc.description }}</p>
        <div v-if="entriesOf(npc.stats).length" class="mt-1.5 flex flex-wrap gap-1.5">
          <ScenarioChip v-for="[k, v] in entriesOf(npc.stats)" :key="k">
            <span class="text-fg-subtle">{{ k }} </span><b class="font-semibold tabular-nums text-fg">{{ v }}</b>
          </ScenarioChip>
        </div>
        <div v-if="entriesOf(npc.skills).length" class="mt-1.5 flex flex-wrap gap-1.5">
          <ScenarioChip v-for="[k, v] in entriesOf(npc.skills)" :key="k">
            <span class="text-fg-subtle">{{ k }} </span><b class="font-semibold tabular-nums text-fg">{{ v }}</b>
          </ScenarioChip>
        </div>
        <div v-if="npc.spells?.length" class="mt-2 text-sm text-fg-muted">法术: {{ npc.spells.join('、') }}</div>
      </li>
    </ul>
  </ScenarioSection>
</template>
