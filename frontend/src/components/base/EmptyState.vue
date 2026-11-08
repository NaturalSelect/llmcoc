<script setup lang="ts">
import { Inbox } from '@lucide/vue'
</script>

<template>
  <div class="flex flex-col items-center gap-3 rounded-xl border border-dashed border-line px-6 py-10 text-center">
    <Inbox :size="28" :stroke-width="1.5" class="text-fg-subtle" />
    <p class="text-sm text-fg-muted"><slot /></p>
    <div v-if="$slots.action"><slot name="action" /></div>
  </div>
</template>
