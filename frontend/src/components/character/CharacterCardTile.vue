<script setup lang="ts">
import { computed } from 'vue'
import { Trash2 } from '@lucide/vue'
import type { CharacterCardFull } from '@/api/characterTypes'
import PaperCard from '@/components/base/PaperCard.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import StatBar from '@/components/base/StatBar.vue'
import { cardSubtitle, hasMadness, madnessLabel, portraitSrc } from './characterLogic'
import StatCell from './StatCell.vue'

const props = defineProps<{ card: CharacterCardFull }>()
const emit = defineEmits<{ open: [card: CharacterCardFull]; delete: [card: CharacterCardFull] }>()

const HEADLINE_STATS = [
  ['STR', 'str'],
  ['CON', 'con'],
  ['DEX', 'dex'],
  ['INT', 'int'],
] as const

// NOTE: 用 Array.from 取首字，避免名字以代理对字符（如罕见汉字）开头时被截成乱码
const initial = computed(() => Array.from(props.card.name?.trim() || '?')[0])
const avatar = computed(() => portraitSrc(props.card) || props.card.avatar_url)
</script>

<template>
  <PaperCard hover tag="article" class="focus-within:border-accent/60">
    <div class="flex items-start gap-3">
      <img v-if="avatar" :src="avatar" alt="" class="h-11 w-11 shrink-0 rounded-full bg-surface-3 object-cover object-top" />
      <div v-else class="flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-accent/15 text-base font-semibold text-accent" aria-hidden="true">
        {{ initial }}
      </div>
      <div class="min-w-0 flex-1">
        <h3 class="truncate text-sm font-semibold text-fg">
          <!-- NOTE: 标题按钮的伪元素铺满整张卡，整卡可点；删除按钮 z-10 压在上面，避免按钮嵌套按钮 -->
          <button type="button" class="max-w-full truncate text-left after:absolute after:inset-0" @click="emit('open', card)">{{ card.name }}</button>
        </h3>
        <p class="mt-0.5 truncate text-xs text-fg-muted">{{ cardSubtitle(card) }}</p>
      </div>
      <button
        type="button"
        class="relative z-10 shrink-0 rounded-lg p-2 text-fg-subtle transition hover:bg-danger/15 hover:text-danger"
        :aria-label="`删除人物卡 ${card.name}`"
        @click="emit('delete', card)"
      >
        <Trash2 :size="16" :stroke-width="1.75" />
      </button>
    </div>

    <div class="mt-4 grid grid-cols-4 gap-2">
      <StatCell v-for="[label, key] in HEADLINE_STATS" :key="key" :label="label" :value="card.stats?.[key]" />
    </div>

    <div class="mt-3 space-y-2">
      <StatBar label="HP" tone="hp" :value="card.stats?.hp ?? 0" :max="card.stats?.max_hp ?? 0" />
      <StatBar label="SAN" tone="san" :value="card.stats?.san ?? 0" :max="card.stats?.max_san ?? 0" />
      <StatBar label="MP" tone="mp" :value="card.stats?.mp ?? 0" :max="card.stats?.max_mp ?? 0" />
    </div>

    <div v-if="hasMadness(card)" class="mt-3">
      <StampBadge tone="seal">{{ madnessLabel(card.madness_state) }}</StampBadge>
    </div>
  </PaperCard>
</template>
