<script setup lang="ts">
import BaseButton from '@/components/base/BaseButton.vue'
import BaseTextarea from '@/components/base/BaseTextarea.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import { balanceRulesLength, BALANCE_RULES_MAX } from './settingsLogic'

const rules = defineModel<string>({ required: true })
defineEmits<{ save: [] }>()
</script>

<template>
  <PaperCard class="max-w-2xl">
    <h3 class="mb-3 font-type text-sm font-bold tracking-wide text-seal">平衡调整规则</h3>
    <p class="mb-3 text-xs text-ink-faint">
      这是站点全局规则，会注入 Lawyer，作为 Lawyer/KP 裁定参考，不是执行层硬拦截；
      留空表示不设置额外平衡规则；下次 Lawyer 调用将读取最新规则。
    </p>
    <BaseTextarea
      v-model="rules"
      rows="6"
      aria-label="平衡调整规则"
      class="min-h-[100px] w-full resize-y font-type !text-xs"
      placeholder="留空表示不设置额外平衡规则..."
    />
    <div class="mt-2 flex items-center justify-between">
      <span class="font-type text-xs" :class="balanceRulesLength(rules) > BALANCE_RULES_MAX ? 'text-seal' : 'text-ink-faint'">
        {{ balanceRulesLength(rules) }}/{{ BALANCE_RULES_MAX }}
      </span>
      <BaseButton class="!px-2.5 !py-1 !text-xs" @click="$emit('save')">保存</BaseButton>
    </div>
  </PaperCard>
</template>
