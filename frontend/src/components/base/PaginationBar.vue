<script setup lang="ts">
import { ref } from 'vue'
import { ChevronLeft, ChevronRight } from '@lucide/vue'

const props = defineProps<{ totalPages: number; total?: number; jump?: boolean }>()
const page = defineModel<number>('page', { required: true })
const draft = ref('')

function go(p: number) {
  page.value = Math.min(Math.max(1, p), Math.max(1, props.totalPages))
}

function submitJump() {
  const text = draft.value.trim()
  draft.value = ''
  if (/^\d+$/.test(text)) go(Number(text))
}
</script>

<template>
  <div v-if="totalPages > 1" class="flex flex-wrap items-center justify-center gap-3 py-3 text-sm text-fg-muted">
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
    <form v-if="jump" class="flex items-center gap-2" @submit.prevent="submitJump">
      <input
        v-model="draft"
        type="text"
        inputmode="numeric"
        autocomplete="off"
        aria-label="跳转到页码"
        :placeholder="`1-${totalPages}`"
        class="h-8 w-20 rounded-lg border border-line bg-surface-2 px-2 text-center text-sm tabular-nums text-fg outline-none transition placeholder:text-fg-subtle focus:border-accent focus:ring-2 focus:ring-accent/30"
      />
      <button
        type="submit"
        class="flex h-8 items-center rounded-lg border border-line px-3 transition hover:bg-surface-2 disabled:opacity-40"
        :disabled="draft.trim() === ''"
      >
        跳转
      </button>
    </form>
  </div>
</template>
