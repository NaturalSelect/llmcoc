<script setup lang="ts">
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
  <PaperCard hover>
    <div class="flex items-start gap-4">
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2">
          <h3 class="font-serif text-base font-bold text-ink">{{ session.name }}</h3>
          <StampBadge :tone="statusTone(status)">{{ statusLabel(status) }}</StampBadge>
          <span v-if="variant === 'lobby' && session.has_password" role="img" aria-label="有密码" class="text-xs">🔒</span>
        </div>
        <p class="mt-1 font-type text-xs text-ink-soft">
          剧本：<span class="text-seal">{{ session.scenario?.name }}</span>
        </p>
        <p class="mt-0.5 font-type text-xs text-ink-faint">
          房主：<span>{{ session.creator?.username }}</span>
          <template v-if="variant === 'lobby'"> · 玩家：<span>{{ playerCountLabel(session) }}</span></template>
          <template v-else> · 玩家：<span>{{ players.length }}</span> 位 · 结束于：<span>{{ fmtDate(session.updated_at) }}</span></template>
        </p>
        <ul v-if="players.length" class="mt-2 flex flex-wrap gap-1.5" aria-label="玩家">
          <li
            v-for="p in players"
            :key="p.id ?? p.user_id"
            class="rounded-sm border border-paper-edge bg-paper-deep/60 px-2 py-0.5 font-type text-xs text-ink-soft"
          >
            <span>{{ p.user?.username }}</span>
            <span class="text-ink-faint"> — </span>
            <span>{{ p.character_card?.name }}</span>
          </li>
        </ul>
      </div>
      <div class="flex shrink-0 flex-col gap-1.5">
        <slot name="actions" />
      </div>
    </div>
  </PaperCard>
</template>
