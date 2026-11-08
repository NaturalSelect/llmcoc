<script setup lang="ts">
import { Lock, Users } from '@lucide/vue'
import { computed } from 'vue'
import type { LobbySession } from '@/api/sessionTypes'
import PaperCard from '@/components/base/PaperCard.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { fmtDate } from '@/utils/format'
import { playerCountLabel, sessionPlayers, statusLabel, statusTone } from './sessionLogic'

// NOTE: archive 是「历史/收藏」里已结束的房间：状态恒为已结束，玩家数只显示人数，并显示结束时间
const props = withDefaults(defineProps<{ session: LobbySession; variant?: 'lobby' | 'archive' }>(), { variant: 'lobby' })

const players = computed(() => sessionPlayers(props.session))
const status = computed(() => (props.variant === 'archive' ? 'ended' : props.session.status))
</script>

<template>
  <PaperCard hover class="flex h-full flex-col gap-3">
    <div class="min-w-0">
      <div class="flex flex-wrap items-center gap-2">
        <h3 class="min-w-0 break-words text-base font-semibold text-fg">{{ session.name }}</h3>
        <StampBadge :tone="statusTone(status)">{{ statusLabel(status) }}</StampBadge>
        <span v-if="variant === 'lobby' && session.has_password" role="img" aria-label="有密码" class="inline-flex text-fg-subtle">
          <Lock :size="16" :stroke-width="1.75" />
        </span>
      </div>
      <p class="mt-1.5 text-sm text-fg-muted">
        剧本：<span class="text-accent">{{ session.scenario?.name }}</span>
      </p>
      <p class="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-fg-subtle">
        <span>房主：<span>{{ session.creator?.username }}</span></span>
        <span v-if="variant === 'lobby'" class="inline-flex items-center gap-1.5 tabular-nums">
          <Users :size="16" :stroke-width="1.75" />玩家：<span>{{ playerCountLabel(session) }}</span>
        </span>
        <template v-else>
          <span class="inline-flex items-center gap-1.5 tabular-nums">
            <Users :size="16" :stroke-width="1.75" />玩家：<span>{{ players.length }}</span> 位
          </span>
          <span class="tabular-nums">结束于：<span>{{ fmtDate(session.updated_at) }}</span></span>
        </template>
      </p>
    </div>

    <ul v-if="players.length" class="flex flex-wrap gap-1.5" aria-label="玩家">
      <li v-for="p in players" :key="p.id ?? p.user_id" class="rounded-full bg-surface-3 px-2.5 py-0.5 text-xs text-fg-muted">
        <span>{{ p.user?.username }}</span>
        <span class="text-fg-subtle"> — </span>
        <span>{{ p.character_card?.name }}</span>
      </li>
    </ul>

    <div v-if="$slots.actions" class="mt-auto flex flex-wrap items-center gap-2 pt-1">
      <slot name="actions" />
    </div>
  </PaperCard>
</template>
