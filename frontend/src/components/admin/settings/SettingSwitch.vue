<script setup lang="ts">
// NOTE: 开关本身不改状态：点击只通知父级，等接口成功后由父级回写，失败时开关保持原位
defineProps<{ modelValue: boolean; label: string; description?: string }>()
defineEmits<{ toggle: [] }>()
</script>

<template>
  <div class="flex items-center justify-between gap-4">
    <div class="min-w-0">
      <span class="block text-sm text-fg">{{ label }}</span>
      <span v-if="description" class="mt-0.5 block text-xs text-fg-subtle">{{ description }}</span>
    </div>
    <button
      type="button"
      role="switch"
      :aria-checked="modelValue"
      :aria-label="label"
      class="relative h-6 w-11 shrink-0 rounded-full border transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      :class="modelValue ? 'border-accent bg-accent' : 'border-line-strong bg-surface-3'"
      @click="$emit('toggle')"
    >
      <span
        class="absolute top-0.5 left-0.5 size-4.5 rounded-full shadow transition-transform"
        :class="modelValue ? 'translate-x-5 bg-accent-fg' : 'bg-fg-muted'"
      />
    </button>
  </div>
</template>
