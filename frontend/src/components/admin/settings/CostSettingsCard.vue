<script setup lang="ts">
import type { SiteSettings } from '@/api/adminTypesContent'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import { COST_FIELDS, type CostKey } from './settingsLogic'

defineProps<{ settings: SiteSettings }>()
defineEmits<{ set: [key: CostKey, value: unknown]; save: [key: CostKey] }>()
</script>

<template>
  <PaperCard class="max-w-md">
    <h3 class="mb-4 font-type text-sm font-bold tracking-wide text-seal">金币与初始配置</h3>
    <div class="space-y-3">
      <div v-for="f in COST_FIELDS" :key="f.key" class="flex items-center justify-between gap-3">
        <label :for="'cost-' + f.key" class="shrink-0 text-sm text-ink">{{ f.label }}</label>
        <div class="flex items-center gap-2">
          <BaseInput
            :id="'cost-' + f.key"
            :model-value="settings[f.key]"
            type="number"
            :min="f.min"
            class="!w-24 !px-2 !py-1 text-right !text-xs"
            @update:model-value="(v) => $emit('set', f.key, v)"
          />
          <BaseButton class="shrink-0 !px-2.5 !py-1 !text-xs" @click="$emit('save', f.key)">保存</BaseButton>
        </div>
      </div>
    </div>
  </PaperCard>
</template>
