<script setup lang="ts">
import { Save } from '@lucide/vue'
import type { SiteSettings } from '@/api/adminTypesContent'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { COST_FIELDS, type CostKey } from './settingsLogic'

defineProps<{ settings: SiteSettings }>()
defineEmits<{ set: [key: CostKey, value: unknown]; save: [key: CostKey] }>()
</script>

<template>
  <PaperCard>
    <SectionHeading>金币与初始配置</SectionHeading>
    <div class="mt-4 space-y-3">
      <div v-for="f in COST_FIELDS" :key="f.key" class="flex items-center justify-between gap-4">
        <label :for="'cost-' + f.key" class="min-w-0 text-sm text-fg">{{ f.label }}</label>
        <div class="flex shrink-0 items-center gap-2">
          <div class="w-28">
            <BaseInput
              :id="'cost-' + f.key"
              :model-value="settings[f.key]"
              type="number"
              :min="f.min"
              class="text-right tabular-nums"
              @update:model-value="(v) => $emit('set', f.key, v)"
            />
          </div>
          <BaseButton size="sm" class="shrink-0" @click="$emit('save', f.key)">
            <Save :size="16" :stroke-width="1.75" />
            保存
          </BaseButton>
        </div>
      </div>
    </div>
  </PaperCard>
</template>
