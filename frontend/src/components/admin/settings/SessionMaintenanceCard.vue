<script setup lang="ts">
import { Power } from '@lucide/vue'
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'

defineEmits<{ endAll: [] }>()
</script>

<template>
  <PaperCard>
    <SectionHeading>房间维护</SectionHeading>
    <div class="mt-4 flex items-center justify-between gap-4">
      <div class="min-w-0">
        <span class="block text-sm text-fg">强制结束所有运行中房间</span>
        <span class="mt-0.5 block text-xs text-fg-subtle">用于后端升级后清理与新版本不兼容的存量房间；不收取结束游戏费用，仅做基础结算与角色恢复</span>
      </div>
      <BaseButton variant="danger" size="sm" class="shrink-0" @click="$emit('endAll')">
        <Power :size="16" :stroke-width="1.75" />
        一键结束
      </BaseButton>
    </div>
  </PaperCard>
</template>
