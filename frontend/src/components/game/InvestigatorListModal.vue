<script setup lang="ts">
import BaseButton from '@/components/base/BaseButton.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import StatBar from '@/components/base/StatBar.vue'
import type { Session, SessionPlayer } from '@/api/types'
import { playerTurnSubmitState } from '@/game/banner'
import { statNum } from '@/game/hud'
import type { Room } from '@/game/roomState'

defineProps<{ open: boolean; session: Session | null; room: Room | null; userId: number | undefined; canJoin?: boolean }>()
const emit = defineEmits<{ close: []; select: [player: SessionPlayer]; join: [] }>()
</script>

<template>
  <BaseModal :open="open" title="调查员状态" max-width="max-w-md" @close="emit('close')">
    <div v-if="!session?.players?.length" class="space-y-3 py-6 text-center">
      <p class="text-sm text-fg-muted">还没有调查员加入，玩家需要携带人物卡加入房间。</p>
      <BaseButton v-if="canJoin" size="sm" @click="emit('join')">带人物卡加入</BaseButton>
    </div>
    <ul v-else class="space-y-2">
      <li v-for="p in session?.players ?? []" :key="p.user_id">
        <button
          type="button"
          class="block w-full rounded-xl bg-surface-2 p-4 text-left transition hover:bg-surface-3 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          @click="emit('select', p)"
        >
          <div class="mb-3 flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-1.5">
                <span class="truncate text-sm font-medium text-fg">{{ p.character_card?.name || p.user?.username }}</span>
                <StampBadge v-if="playerTurnSubmitState(room, p) === 'submitted'" class="shrink-0" tone="moss">已提交</StampBadge>
                <StampBadge v-else-if="playerTurnSubmitState(room, p) === 'pending'" class="shrink-0" tone="faint">待提交</StampBadge>
              </div>
              <p class="mt-0.5 truncate text-xs text-fg-subtle">{{ (p.character_card as { occupation?: string } | null)?.occupation || '未知职业' }}</p>
            </div>
            <StampBadge v-if="p.user_id === userId" class="shrink-0" tone="seal">YOU</StampBadge>
          </div>
          <div v-if="p.character_card" class="space-y-1.5">
            <StatBar label="HP" tone="hp" :value="statNum(p.character_card, 'hp')" :max="statNum(p.character_card, 'max_hp')" />
            <StatBar label="SAN" tone="san" :value="statNum(p.character_card, 'san')" :max="statNum(p.character_card, 'max_san')" />
            <StatBar label="MP" tone="mp" :value="statNum(p.character_card, 'mp')" :max="statNum(p.character_card, 'max_mp')" />
          </div>
        </button>
      </li>
    </ul>
  </BaseModal>
</template>
