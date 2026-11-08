<script setup lang="ts">
import type { RecentSession } from '@/api/characterTypes'
import PaperCard from '@/components/base/PaperCard.vue'
import Skeleton from '@/components/base/Skeleton.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { RECENT_SESSION_LIMIT, playersLabel, sessionStatusLabel, sessionStatusTone } from './dashboardLogic'

defineProps<{ sessions: RecentSession[]; loading: boolean }>()
const emit = defineEmits<{ open: [id: number]; all: [] }>()
</script>

<template>
  <section aria-labelledby="recent-sessions-title">
    <h2 id="recent-sessions-title" class="mb-4 font-serif text-lg font-bold text-ink">最近房间</h2>
    <ul class="space-y-2">
      <li v-for="s in sessions.slice(0, RECENT_SESSION_LIMIT)" :key="s.id">
        <PaperCard hover class="p-3!">
          <div class="flex items-start justify-between gap-2">
            <h3 class="mr-2 truncate text-sm font-semibold leading-tight text-ink">
              <button type="button" class="text-left after:absolute after:inset-0" @click="emit('open', s.id)">{{ s.name }}</button>
            </h3>
            <StampBadge class="shrink-0" :tone="sessionStatusTone(s.status)">{{ sessionStatusLabel(s.status) }}</StampBadge>
          </div>
          <p class="mt-1 font-type text-xs text-ink-soft">{{ s.scenario?.name }}</p>
          <p class="mt-0.5 font-type text-xs text-ink-faint">{{ playersLabel(s) }}</p>
        </PaperCard>
      </li>
      <li v-if="loading && sessions.length === 0" class="space-y-2" aria-busy="true">
        <PaperCard v-for="i in 3" :key="i" class="p-3!">
          <Skeleton class="mb-2 h-3.5 w-2/3" />
          <Skeleton class="h-2.5 w-1/2" />
        </PaperCard>
      </li>
      <li v-if="!loading && sessions.length === 0">
        <PaperCard class="p-4! text-center font-type text-[13px] text-ink-faint">暂无活跃房间</PaperCard>
      </li>
    </ul>
    <button
      type="button"
      class="mt-3 w-full text-center font-type text-xs text-ink-faint transition hover:text-seal"
      @click="emit('all')"
    >
      查看全部房间 →
    </button>
  </section>
</template>
