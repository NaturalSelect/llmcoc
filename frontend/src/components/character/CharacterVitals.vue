<script setup lang="ts">
import type { CharacterCardFull } from '@/api/characterTypes'
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
  { label: '生命值', cur: 'hp', max: 'max_hp', tone: 'text-moss' },
  { label: '理智值', cur: 'san', max: 'max_san', tone: 'text-seal' },
  { label: '魔法值', cur: 'mp', max: 'max_mp', tone: 'text-ledger' },
] as const
</script>

<template>
  <div>
    <div class="mb-4 grid grid-cols-2 gap-1.5 sm:grid-cols-4">
      <StatCell label="角色ID" :value="card.id ?? '—'" />
      <StatCell label="用户ID" :value="card.user_id ?? '—'" />
      <StatCell label="状态" :value="card.is_active ? '启用' : '停用'" />
      <StatCell label="伤势" :value="card.wound_state || 'none'" />
    </div>

    <div class="mb-4 grid grid-cols-2 gap-2">
      <div class="rounded-sm border border-paper-edge bg-paper-deep/40 p-2.5">
        <div class="mb-1 font-type text-xs text-ink-faint">出生地</div>
        <div class="text-sm text-ink">{{ card.birthplace || '—' }}</div>
      </div>
      <div class="rounded-sm border border-paper-edge bg-paper-deep/40 p-2.5">
        <div class="mb-1 font-type text-xs text-ink-faint">现居地</div>
        <div class="text-sm text-ink">{{ card.residence || '—' }}</div>
      </div>
    </div>

    <section v-if="card.avatar_url" class="mb-4">
      <h3 class="mb-1 font-type text-xs font-bold uppercase tracking-wider text-ink-soft">头像链接</h3>
      <p class="break-all font-type text-xs text-ink-faint">{{ card.avatar_url }}</p>
    </section>

    <div class="mb-4 grid grid-cols-4 gap-2">
      <StatCell v-for="[key, label] in ATTRIBUTES" :key="key" big :label="label" :value="card.stats?.[key]" />
    </div>

    <div class="mb-4 grid grid-cols-3 gap-2">
      <div v-for="v in VITALS" :key="v.cur" class="rounded-sm border border-paper-edge bg-paper-deep/40 p-2.5 text-center">
        <div class="mb-1 font-type text-xs text-ink-faint">{{ v.label }}</div>
        <div class="font-type text-xl font-bold" :class="v.tone">{{ (card.stats?.[v.cur] ?? 0) + ' / ' + (card.stats?.[v.max] ?? 0) }}</div>
      </div>
    </div>

    <div class="mb-4 grid grid-cols-4 gap-1.5">
      <StatCell label="移动" :value="card.stats?.mov ?? '—'" />
      <StatCell label="幸运" :value="card.stats?.luck ?? '—'" />
      <StatCell label="体格" :value="card.stats?.build ?? '—'" />
      <StatCell label="伤害加值" :value="card.stats?.db ?? '0'" />
    </div>

    <div class="mb-4 grid grid-cols-2 gap-1.5 sm:grid-cols-4">
      <StatCell label="疯狂持续" :value="card.madness_duration ?? 0" />
      <StatCell label="当日SAN损失" :value="card.daily_san_loss ?? 0" />
      <StatCell label="克苏鲁神话" :value="card.cthulhu_mythos_skill ?? 0" />
      <StatCell label="昏迷" :value="card.is_unconscious ? '是' : '否'" />
    </div>

    <div v-if="hasMadness(card)" class="mb-3 rounded-sm border border-seal/40 bg-seal/5 p-3" role="status">
      <p class="mb-1 font-type text-xs font-bold text-seal">{{ madnessHeading(card.madness_state) }}</p>
      <p v-if="card.madness_symptom" class="text-xs italic text-seal/80">{{ card.madness_symptom }}</p>
    </div>
  </div>
</template>
