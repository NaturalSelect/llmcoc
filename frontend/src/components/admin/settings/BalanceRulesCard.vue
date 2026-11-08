<script setup lang="ts">
import { Save } from '@lucide/vue'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseTextarea from '@/components/base/BaseTextarea.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { balanceRulesLength, BALANCE_RULES_MAX } from './settingsLogic'

const rules = defineModel<string>({ required: true })
defineEmits<{ save: [] }>()
</script>

<template>
  <PaperCard>
    <SectionHeading>平衡调整规则</SectionHeading>
    <p class="mt-1 mb-4 text-sm text-fg-muted">
      这是站点全局规则，会注入 Lawyer，作为 Lawyer/KP 裁定参考，不是执行层硬拦截；
      留空表示不设置额外平衡规则；下次 Lawyer 调用将读取最新规则。
    </p>
    <BaseTextarea
      v-model="rules"
      rows="6"
      aria-label="平衡调整规则"
      placeholder="留空表示不设置额外平衡规则..."
    />
    <div class="mt-3 flex items-center justify-between gap-4">
      <span class="text-xs tabular-nums" :class="balanceRulesLength(rules) > BALANCE_RULES_MAX ? 'text-danger' : 'text-fg-subtle'">
        {{ balanceRulesLength(rules) }}/{{ BALANCE_RULES_MAX }}
      </span>
      <BaseButton size="sm" @click="$emit('save')">
        <Save :size="16" :stroke-width="1.75" />
        保存
      </BaseButton>
    </div>
  </PaperCard>
</template>
