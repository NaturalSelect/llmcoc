<script setup lang="ts">
defineProps<{ text: string }>()
</script>

<template>
  <section class="mb-5 rounded-sm border border-seal/50 bg-seal/10 p-3" aria-label="神话本质">
    <div class="mb-1 font-type text-[10px] font-bold uppercase tracking-wider text-seal">神话本质（永远隐藏）</div>
    <div class="whitespace-pre-wrap font-serif text-xs leading-relaxed text-ink">{{ text }}</div>
  </section>
</template>
