<script setup lang="ts">
import { ChevronLeft, ChevronRight } from '@lucide/vue'

const props = defineProps<{ totalPages: number; total?: number }>()
const page = defineModel<number>('page', { required: true })

function go(p: number) {
  page.value = Math.min(Math.max(1, p), Math.max(1, props.totalPages))
}
</script>

<template>
  <div v-if="totalPages > 1" class="flex items-center justify-center gap-3 py-3 text-sm text-fg-muted">
    <button
      type="button"
      class="flex h-8 w-8 items-center justify-center rounded-lg border border-line transition hover:bg-surface-2 disabled:opacity-40"
      aria-label="上一页"
      :disabled="page <= 1"
      @click="go(page - 1)"
    >
      <ChevronLeft :size="16" :stroke-width="1.75" />
    </button>
    <span class="tabular-nums">{{ page }} / {{ totalPages }}<template v-if="total != null">（共 {{ total }} 条）</template></span>
    <button
      type="button"
      class="flex h-8 w-8 items-center justify-center rounded-lg border border-line transition hover:bg-surface-2 disabled:opacity-40"
      aria-label="下一页"
      :disabled="page >= totalPages"
      @click="go(page + 1)"
    >
      <ChevronRight :size="16" :stroke-width="1.75" />
    </button>
  </div>
</template>
