<script setup lang="ts">
import { AlertTriangle, Crosshair, Footprints, Shield, Skull, Swords } from '@lucide/vue'
import { computed } from 'vue'
import type { HandbookChase, HandbookCombat } from '@/game/handbookTypes'

const props = defineProps<{
  combat: HandbookCombat | null
  chase: HandbookChase | null
}>()

const hasCombat = computed(() => Boolean(props.combat?.active))
const hasChase = computed(() => Boolean(props.chase?.active))

const chaseSlots = computed(() => {
  const ch = props.chase
  if (!ch || !ch.active) return []
  const locs = ch.participants.map((p) => p.location)
  for (const obs of ch.obstacles) {
    locs.push(obs.between[0], obs.between[1])
  }
  if (!locs.length) return []
  const minLoc = Math.min(...locs)
  const maxLoc = Math.max(...locs)
  const slots: Array<{
    index: number
    participants: HandbookChase['participants']
    obstacleAfter: HandbookChase['obstacles'][number] | null
  }> = []
  for (let i = minLoc; i <= maxLoc; i++) {
    const parts = ch.participants.filter((p) => p.location === i)
    const obs = ch.obstacles.find((o) => Math.min(o.between[0], o.between[1]) === i) ?? null
    slots.push({ index: i, participants: parts, obstacleAfter: obs })
  }
  return slots
})
</script>

<template>
  <div v-if="hasCombat || hasChase" class="shrink-0 space-y-2 border-b border-line bg-surface px-4 py-2.5">
    <div v-if="hasCombat && combat" class="mx-auto max-w-3xl space-y-1.5" aria-label="实时战斗行动顺位">
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <span class="inline-flex items-center gap-1.5 font-medium text-danger">
          <Swords :size="14" :stroke-width="1.75" class="shrink-0" />
          战斗中 · 第 {{ combat.round }} 轮
        </span>
        <span class="text-fg-subtle">按敏捷 (DEX) 行动顺位</span>
      </div>
      <div class="flex flex-wrap items-center gap-1.5">
        <div
          v-for="(p, idx) in combat.participants"
          :key="`${p.name}-${idx}`"
          class="inline-flex items-center gap-1.5 rounded-lg border px-2 py-1 text-xs transition"
          :class="
            !p.is_alive
              ? 'border-line bg-surface-2 text-fg-subtle line-through opacity-60'
              : p.pending_clarification
                ? 'border-warning bg-warning/10 text-warning'
                : idx === combat.actor_index && !p.has_acted
                  ? 'border-accent bg-accent/15 text-accent'
                  : p.has_acted
                    ? 'border-line bg-surface-2 text-fg-subtle opacity-70'
                    : 'border-line bg-surface-2 text-fg'
          "
          :title="p.pending_question || undefined"
        >
          <Skull v-if="!p.is_alive" :size="12" :stroke-width="1.75" class="shrink-0" />
          <AlertTriangle v-else-if="p.pending_clarification" :size="12" :stroke-width="1.75" class="shrink-0" />
          <span class="font-medium">{{ p.name }}</span>
          <span class="tabular-nums text-fg-subtle">DEX {{ p.dex }}</span>
          <Crosshair v-if="p.is_aiming" :size="12" :stroke-width="1.75" class="shrink-0 text-accent" title="正在瞄准" />
          <Shield v-if="p.has_dodged_or_fb" :size="12" :stroke-width="1.75" class="shrink-0 text-info" title="本轮已闪避/反击" />
          <span v-if="p.pending_clarification" class="text-warning">待反应</span>
          <span v-else-if="p.has_acted && p.is_alive" class="text-fg-subtle">已动</span>
        </div>
      </div>
    </div>

    <div v-if="hasChase && chase" class="mx-auto max-w-3xl space-y-1.5" aria-label="实时追逐进度">
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <span class="inline-flex items-center gap-1.5 font-medium text-warning">
          <Footprints :size="14" :stroke-width="1.75" class="shrink-0" />
          追逐中 · 第 {{ chase.round }} 轮
        </span>
        <span class="text-fg-subtle">基准 MOV {{ chase.min_mov }}</span>
      </div>
      <div class="flex flex-wrap items-center gap-1.5">
        <template v-for="(slot, sIdx) in chaseSlots" :key="slot.index">
          <div class="inline-flex flex-wrap items-center gap-1 rounded-lg border border-line bg-surface-2 px-2 py-1 text-xs">
            <span class="tabular-nums text-fg-subtle">#{{ slot.index }}</span>
            <span
              v-for="p in slot.participants"
              :key="p.name"
              class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 font-medium"
              :class="p.is_pursuer ? 'bg-danger/15 text-danger' : 'bg-info/15 text-info'"
            >
              {{ p.name }}
              <span class="tabular-nums text-fg-subtle">MOV {{ p.mov }}</span>
            </span>
            <span v-if="!slot.participants.length" class="text-fg-subtle">空位</span>
          </div>
          <span
            v-if="slot.obstacleAfter"
            class="inline-flex items-center gap-1 rounded-md border border-warning/40 bg-warning/10 px-1.5 py-0.5 text-xs text-warning"
            :title="`障碍物 HP ${slot.obstacleAfter.hp}/${slot.obstacleAfter.max_hp}`"
          >
            障碍：{{ slot.obstacleAfter.name }}
          </span>
          <span v-else-if="sIdx < chaseSlots.length - 1" class="text-xs text-fg-subtle" aria-hidden="true">→</span>
        </template>
      </div>
    </div>
  </div>
</template>
