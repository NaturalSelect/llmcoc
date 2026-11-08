<script setup lang="ts">
import { LoaderCircle } from '@lucide/vue'

withDefaults(
  defineProps<{
    variant?: 'primary' | 'secondary' | 'ghost' | 'danger' | 'success'
    size?: 'sm' | 'md'
    type?: 'button' | 'submit'
    disabled?: boolean
    loading?: boolean
  }>(),
  { variant: 'primary', size: 'md', type: 'button', disabled: false, loading: false },
)
</script>

<template>
  <button
    :type="type"
    :disabled="disabled || loading"
    :data-variant="variant"
    class="inline-flex items-center justify-center gap-1.5 rounded-lg border text-sm font-medium whitespace-nowrap transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-50"
    :class="[
      size === 'sm' ? 'h-8 px-3' : 'h-10 px-4',
      {
        'border-accent bg-accent text-accent-fg hover:bg-accent-strong': variant === 'primary',
        'border-line bg-surface-3 text-fg hover:border-line-strong hover:bg-line-strong': variant === 'secondary',
        'border-line bg-transparent text-fg-muted hover:bg-surface-2 hover:text-fg': variant === 'ghost',
        'border-danger/40 bg-danger/15 text-danger hover:bg-danger/25': variant === 'danger',
        'border-success/40 bg-success/15 text-success hover:bg-success/25': variant === 'success',
      },
    ]"
  >
    <LoaderCircle v-if="loading" :size="16" :stroke-width="1.75" class="animate-spin" />
    <slot />
  </button>
</template>
