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
      <li v-for="(npc, i) in npcs" :key="`${npc.name ?? ''}-${i}`" class="rounded-sm border border-paper-edge bg-paper-deep/50 p-2.5">
        <div class="mb-1 flex items-start justify-between gap-2">
          <span class="font-serif text-sm font-bold text-ink">{{ npc.name }}</span>
          <ScenarioChip v-if="npc.attitude" :tone="attitudeTone(npc.attitude)" class="shrink-0">{{ npc.attitude }}</ScenarioChip>
        </div>
        <div v-if="npc.race || npc.occupation || hasMythosSkill(npc.cthulhu_mythos_skill)" class="mb-1.5 flex flex-wrap gap-1">
          <ScenarioChip v-if="npc.race">种族: {{ npc.race }}</ScenarioChip>
          <ScenarioChip v-if="npc.occupation">职业: {{ npc.occupation }}</ScenarioChip>
          <ScenarioChip v-if="hasMythosSkill(npc.cthulhu_mythos_skill)">克苏鲁神话: {{ npc.cthulhu_mythos_skill }}</ScenarioChip>
        </div>
        <p v-if="npc.description" class="mb-1.5 font-serif text-[11px] leading-normal text-ink-soft">{{ npc.description }}</p>
        <div v-if="entriesOf(npc.stats).length" class="mt-1 flex flex-wrap gap-1.5">
          <ScenarioChip v-for="[k, v] in entriesOf(npc.stats)" :key="k">
            <span class="text-ink-faint">{{ k }} </span><b class="text-ink">{{ v }}</b>
          </ScenarioChip>
        </div>
        <div v-if="entriesOf(npc.skills).length" class="mt-1 flex flex-wrap gap-1.5">
          <ScenarioChip v-for="[k, v] in entriesOf(npc.skills)" :key="k">
            <span class="text-ink-faint">{{ k }} </span><b class="text-ink">{{ v }}</b>
          </ScenarioChip>
        </div>
        <div v-if="npc.spells?.length" class="mt-1.5 font-type text-[10px] text-ink-soft">法术: {{ npc.spells.join('、') }}</div>
      </li>
    </ul>
  </ScenarioSection>
</template>
