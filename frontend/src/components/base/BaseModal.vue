<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    title?: string
    maxWidth?: string
    zIndex?: number
  }>(),
  { title: '', maxWidth: 'max-w-lg', zIndex: 50 },
)
const emit = defineEmits<{ close: [] }>()

const panel = ref<HTMLElement | null>(null)
let opener: HTMLElement | null = null

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

// NOTE: 打开时记住触发元素，关闭后把焦点还给它，键盘用户不会丢失位置
watch(
  () => props.open,
  async (open) => {
    if (open) {
      opener = document.activeElement as HTMLElement | null
      document.addEventListener('keydown', onKey)
      await nextTick()
      panel.value?.focus()
    } else {
      document.removeEventListener('keydown', onKey)
      opener?.focus?.()
      opener = null
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => document.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="fixed inset-0 flex items-center justify-center bg-ink/60 p-4"
      :style="{ zIndex }"
      @click.self="emit('close')"
    >
      <div
        ref="panel"
        tabindex="-1"
        role="dialog"
        aria-modal="true"
        :aria-label="title || undefined"
        class="max-h-[90vh] w-full overflow-y-auto rounded-sm border border-paper-edge bg-paper shadow-xl outline-none"
        :class="maxWidth"
      >
        <header v-if="title" class="flex items-center justify-between border-b border-paper-edge px-5 py-3">
          <h2 class="font-serif text-base font-bold text-ink">{{ title }}</h2>
          <button type="button" class="text-ink-faint transition hover:text-seal" aria-label="关闭" @click="emit('close')">✕</button>
        </header>
        <div class="p-5">
          <slot />
        </div>
      </div>
    </div>
  </Teleport>
</template>
