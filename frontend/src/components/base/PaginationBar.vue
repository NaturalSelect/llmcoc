<script setup lang="ts">
const props = defineProps<{ totalPages: number; total?: number }>()
const page = defineModel<number>('page', { required: true })

function go(p: number) {
  page.value = Math.min(Math.max(1, p), Math.max(1, props.totalPages))
}
</script>

<template>
  <div v-if="totalPages > 1" class="flex items-center justify-center gap-3 py-3 font-type text-xs text-ink-soft">
    <button type="button" class="rounded-sm border border-paper-edge px-2 py-1 transition hover:bg-paper-deep disabled:opacity-40" :disabled="page <= 1" @click="go(page - 1)">
      上一页
    </button>
    <span>{{ page }} / {{ totalPages }}<template v-if="total != null">（共 {{ total }} 条）</template></span>
    <button type="button" class="rounded-sm border border-paper-edge px-2 py-1 transition hover:bg-paper-deep disabled:opacity-40" :disabled="page >= totalPages" @click="go(page + 1)">
      下一页
    </button>
  </div>
</template>
