<script setup lang="ts">
import { RefreshCw } from '@lucide/vue'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { regenTitle, type RegenSpec } from './characterLogic'

defineProps<{ spec: RegenSpec; text: string; cost: number; coins: number; busy: boolean }>()
const guidance = defineModel<string>('guidance', { required: true })
const emit = defineEmits<{ regen: [] }>()
</script>

<template>
  <section class="mb-5">
    <div class="mb-2 flex items-center justify-between gap-2">
      <SectionHeading>{{ spec.heading }}</SectionHeading>
      <BaseButton size="sm" variant="secondary" :loading="busy" :disabled="busy || coins < cost" :title="regenTitle(spec, cost, coins)" @click="emit('regen')">
        <template v-if="busy">生成中…</template>
        <template v-else>
          <RefreshCw :size="16" :stroke-width="1.75" />
          重新生成 -{{ cost }}
        </template>
      </BaseButton>
    </div>
    <p class="font-prose text-prose text-fg">{{ text }}</p>
    <BaseInput v-model="guidance" maxlength="200" class="mt-3" :placeholder="spec.placeholder" :aria-label="`${spec.heading}指导`" />
  </section>
</template>
