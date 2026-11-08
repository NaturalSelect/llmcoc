<script setup lang="ts">
defineProps<{ page: number; totalPages: number; total: number }>()
defineEmits<{ prev: []; next: [] }>()
</script>

<template>
  <div class="flex items-center justify-between pt-2 font-type text-xs text-ink-faint">
    <span>共 {{ total }} 个 · 第 {{ page }} / {{ totalPages }} 页</span>
    <div class="flex items-center gap-2">
      <button
        type="button"
        class="rounded-sm border border-paper-edge px-2 py-1 text-ink-soft transition hover:bg-paper-deep disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="page <= 1"
        @click="$emit('prev')"
      >
        上一页
      </button>
      <button
        type="button"
        class="rounded-sm border border-paper-edge px-2 py-1 text-ink-soft transition hover:bg-paper-deep disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="page >= totalPages"
        @click="$emit('next')"
      >
        下一页
      </button>
    </div>
  </div>
</template>
