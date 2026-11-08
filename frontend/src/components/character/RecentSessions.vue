<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import type { RecentSession } from '@/api/characterTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import Skeleton from '@/components/base/Skeleton.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { RECENT_SESSION_LIMIT, playersLabel, sessionStatusLabel, sessionStatusTone } from './dashboardLogic'

defineProps<{ sessions: RecentSession[]; loading: boolean }>()
const emit = defineEmits<{ open: [id: number]; all: [] }>()
</script>

<template>
  <section aria-labelledby="recent-sessions-title">
    <SectionHeading id="recent-sessions-title" class="mb-3">最近房间</SectionHeading>
    <ul class="space-y-2">
      <li v-for="s in sessions.slice(0, RECENT_SESSION_LIMIT)" :key="s.id">
        <PaperCard hover padding="sm">
          <div class="flex items-start justify-between gap-2">
            <h3 class="min-w-0 truncate text-sm font-semibold leading-snug text-fg">
              <button type="button" class="max-w-full truncate text-left after:absolute after:inset-0" @click="emit('open', s.id)">{{ s.name }}</button>
            </h3>
            <StampBadge class="shrink-0" :tone="sessionStatusTone(s.status)">{{ sessionStatusLabel(s.status) }}</StampBadge>
          </div>
          <p class="mt-1 truncate text-xs text-fg-muted">{{ s.scenario?.name }}</p>
          <p class="mt-0.5 text-xs tabular-nums text-fg-subtle">{{ playersLabel(s) }}</p>
        </PaperCard>
      </li>
      <li v-if="loading && sessions.length === 0" class="space-y-2" aria-busy="true">
        <PaperCard v-for="i in 3" :key="i" padding="sm">
          <Skeleton class="mb-2 h-3.5 w-2/3" />
          <Skeleton class="h-2.5 w-1/2" />
        </PaperCard>
      </li>
      <li v-if="!loading && sessions.length === 0">
        <EmptyState>暂无活跃房间</EmptyState>
      </li>
    </ul>
    <BaseButton variant="ghost" size="sm" class="mt-3 w-full" @click="emit('all')">
      查看全部房间
      <ArrowRight :size="16" :stroke-width="1.75" />
    </BaseButton>
  </section>
</template>
