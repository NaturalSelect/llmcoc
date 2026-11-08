<script setup lang="ts">
import { computed, ref, type Component } from 'vue'
import { ChevronDown, ChevronUp, Info, Megaphone, TriangleAlert } from '@lucide/vue'
import type { Announcement } from '@/api/characterTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { fmtDate } from '@/utils/format'
import { renderMarkdown } from '@/utils/markdown'
import { announcementToggleLabel, visibleAnnouncements } from './dashboardLogic'

const props = defineProps<{ announcements: Announcement[] }>()
const expanded = ref(false)

const shown = computed(() => visibleAnnouncements(props.announcements, expanded.value))

// NOTE: 只有 important / warning 有强调色，其余等级（含未知值）一律当普通公告；等级只用图标和文字色表达，不再用左侧色条
function levelOf(level: string | undefined): { icon: Component; tint: string } {
  if (level === 'important') return { icon: Info, tint: 'text-info' }
  if (level === 'warning') return { icon: TriangleAlert, tint: 'text-warning' }
  return { icon: Megaphone, tint: 'text-fg-subtle' }
}
</script>

<template>
  <section v-if="announcements.length > 0" aria-labelledby="announcements-title">
    <SectionHeading id="announcements-title" class="mb-3">公告</SectionHeading>
    <ul class="space-y-2">
      <li v-for="a in shown" :key="a.id">
        <PaperCard padding="sm">
          <div class="flex items-start justify-between gap-2">
            <span class="flex min-w-0 items-start gap-2 text-sm font-semibold leading-snug text-fg">
              <component :is="levelOf(a.level).icon" :size="16" :stroke-width="1.75" class="mt-0.5 shrink-0" :class="levelOf(a.level).tint" />
              <span class="min-w-0">{{ a.title }}</span>
            </span>
            <StampBadge v-if="a.is_pinned" tone="seal" class="shrink-0">置顶</StampBadge>
          </div>
          <p class="mt-1 text-xs tabular-nums text-fg-subtle">{{ fmtDate(a.starts_at || a.created_at) }}</p>
          <!-- NOTE: 公告正文是后台编写的 Markdown，只经 renderMarkdown（marked + DOMPurify）转成 HTML -->
          <div class="announcement-md mt-2 max-h-40 overflow-y-auto pr-1 text-sm" v-html="renderMarkdown(a.content)" />
        </PaperCard>
      </li>
    </ul>
    <BaseButton v-if="announcements.length > 3" variant="ghost" size="sm" class="mt-3 w-full" :aria-expanded="expanded" @click="expanded = !expanded">
      {{ announcementToggleLabel(expanded, announcements.length) }}
      <component :is="expanded ? ChevronUp : ChevronDown" :size="16" :stroke-width="1.75" />
    </BaseButton>
  </section>
</template>
