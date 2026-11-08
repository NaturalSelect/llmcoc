<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Announcement } from '@/api/characterTypes'
import StampBadge from '@/components/base/StampBadge.vue'
import { fmtDate } from '@/utils/format'
import { renderMarkdown } from '@/utils/markdown'
import { announcementAccent, announcementToggleLabel, visibleAnnouncements } from './dashboardLogic'

const props = defineProps<{ announcements: Announcement[] }>()
const expanded = ref(false)

const shown = computed(() => visibleAnnouncements(props.announcements, expanded.value))
</script>

<template>
  <section v-if="announcements.length > 0" class="mb-6" aria-labelledby="announcements-title">
    <h2 id="announcements-title" class="mb-4 font-serif text-lg font-bold text-ink">公告</h2>
    <ul class="space-y-2">
      <li
        v-for="a in shown"
        :key="a.id"
        class="rounded-sm border border-l-4 border-paper-edge bg-paper p-3"
        :class="announcementAccent(a.level)"
      >
        <div class="flex items-start justify-between gap-2">
          <span class="text-sm font-semibold leading-tight" :class="a.level === 'warning' ? 'text-seal' : 'text-ink'">{{ a.title }}</span>
          <StampBadge v-if="a.is_pinned" tone="seal" class="shrink-0">置顶</StampBadge>
        </div>
        <p class="mt-0.5 font-type text-xs text-ink-faint">{{ fmtDate(a.starts_at || a.created_at) }}</p>
        <!-- NOTE: 公告正文是后台编写的 Markdown，只经 renderMarkdown（marked + DOMPurify）转成 HTML -->
        <div class="announcement-md mt-2 max-h-40 overflow-y-auto text-sm" v-html="renderMarkdown(a.content)" />
      </li>
    </ul>
    <button
      v-if="announcements.length > 3"
      type="button"
      class="mt-3 w-full text-center font-type text-xs text-ink-faint transition hover:text-seal"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      {{ announcementToggleLabel(expanded, announcements.length) }}
    </button>
  </section>
</template>

<style scoped>
/* NOTE: Tailwind preflight 会清空标题和列表的默认样式，Markdown 渲染出的标签需要重新排版 */
.announcement-md {
  line-height: 1.6;
  color: var(--color-ink-soft);
}
.announcement-md :deep(> * + *) {
  margin-top: 0.5em;
}
.announcement-md :deep(h1),
.announcement-md :deep(h2),
.announcement-md :deep(h3) {
  font-weight: 700;
  color: var(--color-ink);
}
.announcement-md :deep(h1) {
  font-size: 1.05rem;
}
.announcement-md :deep(h2) {
  font-size: 1rem;
}
.announcement-md :deep(h3) {
  font-size: 0.9rem;
}
.announcement-md :deep(ul) {
  list-style: disc;
  padding-left: 1.25em;
}
.announcement-md :deep(ol) {
  list-style: decimal;
  padding-left: 1.25em;
}
.announcement-md :deep(a) {
  color: var(--color-ledger);
  text-decoration: underline;
}
.announcement-md :deep(code) {
  background: var(--color-paper-deep);
  padding: 1px 4px;
  border-radius: 2px;
  font-family: var(--font-type);
  font-size: 0.85em;
}
.announcement-md :deep(pre) {
  background: var(--color-paper-deep);
  padding: 0.5em 0.75em;
  border-radius: 2px;
  overflow-x: auto;
}
.announcement-md :deep(pre code) {
  background: none;
  padding: 0;
}
.announcement-md :deep(blockquote) {
  border-left: 2px solid var(--color-paper-edge);
  padding-left: 0.75em;
  color: var(--color-ink-faint);
}
.announcement-md :deep(img) {
  max-width: 100%;
}
</style>
