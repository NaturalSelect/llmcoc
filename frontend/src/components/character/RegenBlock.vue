<script setup lang="ts">
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import { regenTitle, type RegenSpec } from './characterLogic'

defineProps<{ spec: RegenSpec; text: string; cost: number; coins: number; busy: boolean }>()
const guidance = defineModel<string>('guidance', { required: true })
const emit = defineEmits<{ regen: [] }>()
</script>

<template>
  <section class="mb-3">
    <div class="mb-1 flex items-center gap-2">
      <h3 class="font-type text-xs font-bold uppercase tracking-wider text-ink-soft">{{ spec.heading }}</h3>
      <BaseButton
        class="px-2! py-0.5! text-xs!"
        :disabled="busy || coins < cost"
        :title="regenTitle(spec, cost, coins)"
        @click="emit('regen')"
      >
        <template v-if="busy">生成中…</template>
        <template v-else>重新生成 -{{ cost }}</template>
      </BaseButton>
    </div>
    <p class="text-sm leading-relaxed text-ink-soft">{{ text }}</p>
    <BaseInput v-model="guidance" maxlength="200" class="mt-1.5 text-xs!" :placeholder="spec.placeholder" :aria-label="`${spec.heading}指导`" />
  </section>
</template>
