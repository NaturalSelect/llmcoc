<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: 'primary' | 'ghost' | 'danger' | 'success'
    type?: 'button' | 'submit'
    disabled?: boolean
    loading?: boolean
  }>(),
  { variant: 'primary', type: 'button', disabled: false, loading: false },
)
</script>

<template>
  <button
    :type="type"
    :disabled="disabled || loading"
    class="inline-flex items-center justify-center gap-1.5 rounded-sm border px-4 py-2 font-serif text-sm transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ledger disabled:cursor-not-allowed disabled:opacity-50"
    :class="{
      'border-seal bg-seal text-paper hover:bg-seal/90': variant === 'primary',
      'border-paper-edge bg-transparent text-ink-soft hover:bg-paper-deep': variant === 'ghost',
      'border-seal bg-transparent text-seal hover:bg-seal/10': variant === 'danger',
      'border-moss bg-moss text-paper hover:bg-moss/90': variant === 'success',
    }"
  >
    <slot />
  </button>
</template>
