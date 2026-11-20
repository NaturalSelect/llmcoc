<script setup lang="ts">
import { Bot, UserMinus } from '@lucide/vue'
import BaseButton from '@/components/base/BaseButton.vue'

// NOTE: 大厅阶段房主（或管理员）对 AI 陪玩的增删入口，右侧栏和调查员弹窗共用；显不显示由 game/companion.ts 的 canAddCompanion / canRemoveCompanion 决定，
// 点击后的确认、请求和刷新都在游戏页里完成
defineProps<{ canAdd: boolean; canRemove: boolean; busy?: boolean }>()
const emit = defineEmits<{ add: []; remove: [] }>()
</script>

<template>
  <div v-if="canAdd || canRemove" class="flex flex-wrap items-center gap-2">
    <BaseButton v-if="canAdd" size="sm" variant="secondary" @click="emit('add')">
      <Bot :size="16" :stroke-width="1.75" />
      添加 AI 陪玩
    </BaseButton>
    <BaseButton v-if="canRemove" size="sm" variant="danger" :loading="busy" @click="emit('remove')">
      <UserMinus :size="16" :stroke-width="1.75" />
      移除 AI
    </BaseButton>
  </div>
</template>
