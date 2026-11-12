<script setup lang="ts">
import { Save } from '@lucide/vue'
import type { SiteSettings } from '@/api/adminTypesContent'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { LAWYER_SIMILARITY_MAX, LAWYER_SIMILARITY_MIN } from './settingsLogic'

defineProps<{ settings: SiteSettings }>()
defineEmits<{ set: [value: unknown]; save: [] }>()
</script>

<template>
  <PaperCard>
    <SectionHeading>Lawyer 缓存相似度阈值</SectionHeading>
    <p class="mt-1 mb-4 text-sm text-fg-muted">
      仅在启用 embedding 角色后生效；值越高越严格、命中越少。取值范围 {{ LAWYER_SIMILARITY_MIN }} 到 {{ LAWYER_SIMILARITY_MAX }}，默认 0.3。
    </p>
    <div class="flex items-center justify-between gap-4">
      <label for="lawyer-cache-min-similarity" class="min-w-0 text-sm text-fg">最低相似度</label>
      <div class="flex shrink-0 items-center gap-2">
        <div class="w-28">
          <BaseInput
            id="lawyer-cache-min-similarity"
            :model-value="settings.lawyer_cache_min_similarity"
            type="number"
            step="0.05"
            :min="LAWYER_SIMILARITY_MIN"
            :max="LAWYER_SIMILARITY_MAX"
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
