<script setup lang="ts">
import type { Entry } from './characterLogic'

defineProps<{ entries: Entry[]; busy?: boolean }>()
const emit = defineEmits<{ remove: [key: string | undefined] }>()
</script>

<template>
  <ul v-if="entries.length" class="space-y-1.5">
    <li v-for="(e, idx) in entries" :key="idx" class="flex items-start justify-between rounded-sm border border-paper-edge bg-paper-deep/40 p-2">
      <div class="min-w-0">
        <p class="text-xs font-bold text-ink">{{ e.title }}</p>
        <p v-for="(line, i) in e.lines" :key="i" class="text-xs text-ink-soft">{{ line }}</p>
      </div>
      <button
        type="button"
        class="ml-2 shrink-0 text-xs leading-none text-seal transition hover:underline disabled:opacity-40"
        :disabled="busy"
        :aria-label="`删除 ${e.title}`"
        @click="emit('remove', e.key)"
      >
        删除
      </button>
    </li>
  </ul>
  <p v-else class="font-type text-xs text-ink-faint">—</p>
</template>
