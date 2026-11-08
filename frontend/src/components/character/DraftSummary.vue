<script setup lang="ts">
import type { RollDraft } from '@/api/characterTypes'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { fmtDate } from '@/utils/format'
import StatCell from './StatCell.vue'

defineProps<{ draft: RollDraft }>()

const PRIMARY = ['STR', 'CON', 'SIZ', 'DEX', 'APP', 'INT', 'POW', 'EDU'] as const
</script>

<template>
  <div class="space-y-3">
    <div class="flex items-center justify-between gap-2">
      <SectionHeading>已锁定属性</SectionHeading>
      <span class="text-xs tabular-nums text-fg-subtle">{{ draft.expires_at ? '过期时间：' + fmtDate(draft.expires_at) : '' }}</span>
    </div>
    <div class="grid grid-cols-4 gap-2 md:grid-cols-8">
      <StatCell v-for="k in PRIMARY" :key="k" :label="k" :value="draft.stats?.[k.toLowerCase()] as number | undefined" />
    </div>
    <div class="grid grid-cols-3 gap-2 md:grid-cols-6">
      <StatCell label="Luck" :value="draft.stats?.luck" />
      <StatCell label="HP" :value="`${draft.stats?.hp}/${draft.stats?.max_hp}`" />
      <StatCell label="MP" :value="`${draft.stats?.mp}/${draft.stats?.max_mp}`" />
      <StatCell label="SAN" :value="`${draft.stats?.san}/${draft.stats?.max_san}`" />
      <StatCell label="MOV" :value="draft.stats?.mov" />
      <StatCell label="DB/Build" :value="`${draft.stats?.db || '0'}/${draft.stats?.build ?? 0}`" />
    </div>
    <ul class="space-y-1 text-xs text-fg-subtle">
      <li v-for="(line, i) in draft.raw_rolls?.age_log || []" :key="i">· {{ line }}</li>
    </ul>
  </div>
</template>
