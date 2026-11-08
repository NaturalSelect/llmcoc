<script setup lang="ts">
import BaseModal from '@/components/base/BaseModal.vue'
import StatBar from '@/components/base/StatBar.vue'
import type { Session, SessionPlayer } from '@/api/types'
import { playerTurnSubmitState } from '@/game/banner'
import { statNum } from '@/game/hud'
import type { Room } from '@/game/roomState'

defineProps<{ open: boolean; session: Session | null; room: Room | null; userId: number | undefined }>()
const emit = defineEmits<{ close: []; select: [player: SessionPlayer] }>()
</script>

<template>
  <BaseModal :open="open" title="调查员状态" max-width="max-w-md" @close="emit('close')">
    <ul class="max-h-[60vh] space-y-3 overflow-y-auto pr-1">
      <li v-for="p in session?.players ?? []" :key="p.user_id">
        <button
          type="button"
          class="block w-full rounded-sm border border-paper-edge bg-paper-deep/40 p-3 text-left transition hover:border-seal/60 hover:bg-paper-deep"
          @click="emit('select', p)"
        >
          <div class="mb-2 flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-1.5">
                <span class="truncate text-sm font-bold text-ink">{{ p.character_card?.name || p.user?.username }}</span>
                <span
                  v-if="playerTurnSubmitState(room, p) === 'submitted'"
                  class="shrink-0 rounded-sm border border-moss px-1 font-type text-[10px] text-moss"
                >
                  已提交
                </span>
                <span
                  v-else-if="playerTurnSubmitState(room, p) === 'pending'"
                  class="shrink-0 rounded-sm border border-ink-faint px-1 font-type text-[10px] text-ink-faint"
                >
                  待提交
                </span>
              </div>
              <p class="mt-0.5 truncate text-[11px] text-ink-faint">{{ (p.character_card as { occupation?: string } | null)?.occupation || '未知职业' }}</p>
            </div>
            <span v-if="p.user_id === userId" class="shrink-0 rounded-sm border border-seal/50 px-1 font-type text-[9px] text-seal">YOU</span>
          </div>
          <div v-if="p.character_card" class="space-y-1">
            <StatBar label="HP" tone="moss" :value="statNum(p.character_card, 'hp')" :max="statNum(p.character_card, 'max_hp')" />
            <StatBar label="SAN" tone="seal" :value="statNum(p.character_card, 'san')" :max="statNum(p.character_card, 'max_san')" />
            <StatBar label="MP" tone="ledger" :value="statNum(p.character_card, 'mp')" :max="statNum(p.character_card, 'max_mp')" />
          </div>
        </button>
      </li>
    </ul>
  </BaseModal>
</template>
