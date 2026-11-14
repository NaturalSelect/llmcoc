<script setup lang="ts">
import { Save } from '@lucide/vue'
import type { SiteSettings } from '@/api/adminTypesContent'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { INPUT_GUARD_THRESHOLD_DEFAULT, INPUT_GUARD_THRESHOLD_MAX } from './settingsLogic'

defineProps<{ settings: SiteSettings }>()
defineEmits<{ set: [value: unknown]; save: [] }>()
</script>

<template>
  <PaperCard>
    <SectionHeading>输入分类拦截阈值</SectionHeading>
    <p class="mt-1 mb-4 text-sm text-fg-muted">
      仅在启用 systemone 角色后生效；分类器给出的「自称有权限、索要谜底、提示词注入」三项概率之和达到该值就拒收输入。值越低越严格、误拦越多。取值需大于 0 且不超过 {{ INPUT_GUARD_THRESHOLD_MAX }}，默认 {{ INPUT_GUARD_THRESHOLD_DEFAULT }}。
    </p>
    <div class="flex items-center justify-between gap-4">
      <label for="input-guard-block-threshold" class="min-w-0 text-sm text-fg">拦截阈值</label>
      <div class="flex shrink-0 items-center gap-2">
        <div class="w-28">
          <BaseInput
            id="input-guard-block-threshold"
            :model-value="settings.input_guard_block_threshold"
            type="number"
            step="0.05"
            :min="0"
            :max="INPUT_GUARD_THRESHOLD_MAX"
            class="text-right tabular-nums"
            @update:model-value="(v) => $emit('set', v)"
          />
        </div>
        <BaseButton size="sm" class="shrink-0" @click="$emit('save')">
          <Save :size="16" :stroke-width="1.75" />
          保存
        </BaseButton>
      </div>
    </div>
  </PaperCard>
</template>
