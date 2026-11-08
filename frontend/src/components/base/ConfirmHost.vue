<script setup lang="ts">
import { computed } from 'vue'
import BaseButton from '@/components/base/BaseButton.vue'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
const state = computed(() => ui.confirmState)

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') ui.resolveConfirm(false)
  // NOTE: 输入框里按回车也算确认，与旧前端一致；多行文本框不会走到这里
  else if (e.key === 'Enter') ui.resolveConfirm(true)
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="state"
      class="fixed inset-0 z-[60] flex items-center justify-center bg-bg/80 p-4 backdrop-blur-sm"
      @click.self="ui.resolveConfirm(false)"
      @keydown="onKey"
    >
      <div
        class="w-full max-w-sm rounded-xl border border-line bg-surface p-6 shadow-panel"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
      >
        <h2 id="confirm-title" class="text-sm font-medium text-fg-muted">确认操作</h2>
        <p class="mt-3 whitespace-pre-wrap leading-relaxed text-fg">{{ state.message }}</p>
        <input
          v-if="state.withInput"
          v-model="state.inputValue"
          class="mt-3 h-10 w-full rounded-lg border border-line bg-surface-2 px-3 text-sm text-fg outline-none placeholder:text-fg-subtle focus:border-accent focus:ring-2 focus:ring-accent/30"
          :placeholder="state.inputPlaceholder"
          autofocus
        />
        <div class="mt-5 flex gap-2">
          <BaseButton variant="ghost" class="flex-1" @click="ui.resolveConfirm(false)">取消</BaseButton>
          <BaseButton :variant="state.danger ? 'danger' : 'primary'" class="flex-1" autofocus @click="ui.resolveConfirm(true)">
            {{ state.confirmText }}
          </BaseButton>
        </div>
      </div>
    </div>
  </Teleport>
</template>
