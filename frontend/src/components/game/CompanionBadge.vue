<script setup lang="ts">
import { Bot } from '@lucide/vue'
import StampBadge from '@/components/base/StampBadge.vue'

// NOTE: AI 陪玩席位的徽标，游戏页玩家列表、调查员弹窗和大厅会话卡共用；class 透传给 StampBadge（例如 shrink-0）
</script>

<template>
  <StampBadge tone="ledger" class="gap-1" title="AI 陪玩">
    <Bot :size="12" :stroke-width="1.75" class="shrink-0" aria-hidden="true" />
    AI
  </StampBadge>
</template>
