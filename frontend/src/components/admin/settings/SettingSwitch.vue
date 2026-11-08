<script setup lang="ts">
// NOTE: 开关本身不改状态：点击只通知父级，等接口成功后由父级回写，失败时开关保持原位
defineProps<{ modelValue: boolean; label: string; description?: string }>()
defineEmits<{ toggle: [] }>()
</script>

<template>
  <div class="flex items-center justify-between gap-3">
    <div>
      <span class="block text-sm text-ink">{{ label }}</span>
      <span v-if="description" class="text-xs text-ink-faint">{{ description }}</span>
    </div>
    <button
      type="button"
      role="switch"
      :aria-checked="modelValue"
      :aria-label="label"
      class="relative h-6 w-12 shrink-0 rounded-full border transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ledger"
      :class="modelValue ? 'border-seal bg-seal' : 'border-paper-edge bg-paper-edge'"
      @click="$emit('toggle')"
    >
      <span
        class="absolute top-0.5 left-0.5 h-4.5 w-4.5 rounded-full bg-paper shadow transition-transform"
        :class="modelValue ? 'translate-x-6' : ''"
      />
    </button>
  </div>
</template>
