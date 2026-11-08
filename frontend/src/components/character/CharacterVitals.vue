<script setup lang="ts">
import { TriangleAlert } from '@lucide/vue'
import type { CharacterCardFull } from '@/api/characterTypes'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { hasMadness, madnessHeading } from './characterLogic'
import StatCell from './StatCell.vue'

defineProps<{ card: CharacterCardFull }>()

const ATTRIBUTES = [
  ['str', '力量'],
  ['con', '体质'],
  ['siz', '体型'],
  ['dex', '敏捷'],
  ['app', '外貌'],
  ['int', '智识'],
  ['pow', '意志'],
  ['edu', '教育'],
] as const

const VITALS = [
  { label: '生命值', cur: 'hp', max: 'max_hp', tone: 'text-hp' },
  { label: '理智值', cur: 'san', max: 'max_san', tone: 'text-san' },
  { label: '魔法值', cur: 'mp', max: 'max_mp', tone: 'text-mp' },
] as const

// NOTE: madnessHeading 的结果带 emoji 前缀（逻辑文件及其测试不在这里改），模板里只取文字部分，图标改用 SVG
function madnessText(state: string | undefined): string {
  return madnessHeading(state).replace(/^\S+\s+/, '')
}
</script>

<template>
  <div class="space-y-4">
    <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
      <StatCell label="角色ID" :value="card.id ?? '—'" />
      <StatCell label="用户ID" :value="card.user_id ?? '—'" />
      <StatCell label="状态" :value="card.is_active ? '启用' : '停用'" />
      <StatCell label="伤势" :value="card.wound_state || 'none'" />
    </div>

    <div class="grid grid-cols-2 gap-2">
      <div class="rounded-lg bg-surface-2 px-3 py-2">
        <div class="text-xs text-fg-subtle">出生地</div>
        <div class="text-sm text-fg">{{ card.birthplace || '—' }}</div>
      </div>
      <div class="rounded-lg bg-surface-2 px-3 py-2">
        <div class="text-xs text-fg-subtle">现居地</div>
        <div class="text-sm text-fg">{{ card.residence || '—' }}</div>
      </div>
    </div>

    <section v-if="card.avatar_url">
      <SectionHeading class="mb-1">头像链接</SectionHeading>
      <p class="break-all text-xs text-fg-subtle">{{ card.avatar_url }}</p>
    </section>

    <div class="grid grid-cols-4 gap-2">
      <StatCell v-for="[key, label] in ATTRIBUTES" :key="key" big :label="label" :value="card.stats?.[key]" />
    </div>

    <div class="grid grid-cols-3 gap-2">
      <div v-for="v in VITALS" :key="v.cur" class="rounded-lg bg-surface-2 px-3 py-2 text-center">
        <div class="text-xs text-fg-subtle">{{ v.label }}</div>
        <div class="text-xl font-semibold tabular-nums" :class="v.tone">{{ (card.stats?.[v.cur] ?? 0) + ' / ' + (card.stats?.[v.max] ?? 0) }}</div>
      </div>
    </div>

    <div class="grid grid-cols-4 gap-2">
      <StatCell label="移动" :value="card.stats?.mov ?? '—'" />
      <StatCell label="幸运" :value="card.stats?.luck ?? '—'" />
      <StatCell label="体格" :value="card.stats?.build ?? '—'" />
      <StatCell label="伤害加值" :value="card.stats?.db ?? '0'" />
    </div>

    <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
      <StatCell label="疯狂持续" :value="card.madness_duration ?? 0" />
      <StatCell label="当日SAN损失" :value="card.daily_san_loss ?? 0" />
      <StatCell label="克苏鲁神话" :value="card.cthulhu_mythos_skill ?? 0" />
      <StatCell label="昏迷" :value="card.is_unconscious ? '是' : '否'" />
    </div>

    <div v-if="hasMadness(card)" class="rounded-xl bg-warning/10 p-3" role="status">
      <p class="flex items-center gap-2 text-sm font-medium text-warning">
        <TriangleAlert :size="16" :stroke-width="1.75" />
        {{ madnessText(card.madness_state) }}
      </p>
      <p v-if="card.madness_symptom" class="mt-1 text-xs text-fg-muted">{{ card.madness_symptom }}</p>
    </div>
  </div>
</template>
