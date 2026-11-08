<script setup lang="ts">
import { CircleAlert, CircleCheck } from '@lucide/vue'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
</script>

<template>
  <Teleport to="body">
    <div class="pointer-events-none fixed right-4 top-4 z-[80] flex flex-col items-end gap-2" aria-live="polite">
      <TransitionGroup name="toast">
        <div
          v-for="t in ui.toasts"
          :key="t.id"
          class="pointer-events-auto flex max-w-sm items-start gap-2 rounded-xl border bg-surface-2 px-4 py-3 text-sm text-fg shadow-panel"
          :class="t.type === 'error' ? 'border-danger/50' : 'border-success/50'"
          role="status"
        >
          <CircleAlert v-if="t.type === 'error'" :size="16" :stroke-width="1.75" class="mt-0.5 shrink-0 text-danger" />
          <CircleCheck v-else :size="16" :stroke-width="1.75" class="mt-0.5 shrink-0 text-success" />
          <span>{{ t.message }}</span>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-enter-active,
.toast-leave-active {
  transition:
    opacity 0.2s ease,
    transform 0.2s ease;
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}
</style>
