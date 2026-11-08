<script setup lang="ts">
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'

defineEmits<{ endAll: [] }>()
</script>

<template>
  <PaperCard class="max-w-md">
    <h3 class="mb-4 font-type text-sm font-bold tracking-wide text-seal">房间维护</h3>
    <div class="flex items-center justify-between gap-3">
      <div>
        <span class="block text-sm text-ink">强制结束所有运行中房间</span>
        <span class="text-xs text-ink-faint">用于后端升级后清理与新版本不兼容的存量房间；不收取结束游戏费用，仅做基础结算与角色恢复</span>
      </div>
      <BaseButton variant="danger" class="shrink-0 !px-2.5 !py-1 !text-xs" @click="$emit('endAll')">一键结束</BaseButton>
    </div>
  </PaperCard>
</template>
