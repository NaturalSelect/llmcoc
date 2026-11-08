<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import BaseButton from '@/components/base/BaseButton.vue'
import type { Entry } from './characterLogic'

defineProps<{ entries: Entry[]; busy?: boolean }>()
const emit = defineEmits<{ remove: [key: string | undefined] }>()
</script>

<template>
  <ul v-if="entries.length" class="space-y-2">
    <li v-for="(e, idx) in entries" :key="idx" class="flex items-start justify-between gap-3 rounded-lg bg-surface-2 px-3 py-2">
      <div class="min-w-0">
        <p class="text-sm font-medium text-fg">{{ e.title }}</p>
        <p v-for="(line, i) in e.lines" :key="i" class="text-xs text-fg-muted">{{ line }}</p>
      </div>
      <BaseButton variant="danger" size="sm" class="shrink-0" :disabled="busy" :aria-label="`删除 ${e.title}`" @click="emit('remove', e.key)">
        <Trash2 :size="16" :stroke-width="1.75" />
        删除
      </BaseButton>
    </li>
  </ul>
  <p v-else class="text-xs text-fg-subtle">—</p>
</template>
