<script setup lang="ts">
import { computed } from 'vue'
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
      class="fixed inset-0 z-[60] flex items-center justify-center bg-ink/60 p-4"
      @click.self="ui.resolveConfirm(false)"
      @keydown="onKey"
    >
      <div
        class="w-full max-w-sm rounded-sm border border-paper-edge bg-paper p-6 shadow-xl"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
      >
        <h2 id="confirm-title" class="font-type text-sm tracking-widest text-ink-soft">确认操作</h2>
        <p class="mt-3 whitespace-pre-wrap font-serif leading-relaxed text-ink">{{ state.message }}</p>
        <input
          v-if="state.withInput"
          v-model="state.inputValue"
          class="mt-3 w-full rounded-sm border border-paper-edge bg-paper-deep px-3 py-2 font-serif text-ink outline-none focus:border-seal"
          :placeholder="state.inputPlaceholder"
          autofocus
        />
        <div class="mt-5 flex gap-2">
          <button
            type="button"
            class="flex-1 rounded-sm border border-paper-edge px-3 py-2 font-serif text-ink-soft transition hover:bg-paper-deep"
            @click="ui.resolveConfirm(false)"
          >
            取消
          </button>
          <button
            type="button"
            class="flex-1 rounded-sm px-3 py-2 font-serif text-paper transition hover:opacity-90"
            :class="state.danger ? 'bg-seal' : 'bg-ledger'"
            autofocus
            @click="ui.resolveConfirm(true)"
          >
            {{ state.confirmText }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
