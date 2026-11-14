<script setup lang="ts">
import { ref } from 'vue'

const el = ref<HTMLInputElement | null>(null)

// NOTE: 不用 v-model：文件框的值只能由用户选取或整体清空，所以点击时现读 files[0]，上传成功后调 clear()
defineExpose({
  file: (): File | null => el.value?.files?.[0] ?? null,
  clear: () => {
    if (el.value) el.value.value = ''
  },
})
</script>

<template>
  <input
    ref="el"
    type="file"
    class="block w-full min-w-0 rounded-lg border border-line bg-surface-2 px-3 py-2 text-sm text-fg file:mr-3 file:rounded-lg file:border-0 file:bg-surface-3 file:px-2 file:py-1 file:text-xs file:text-fg"
  />
</template>
