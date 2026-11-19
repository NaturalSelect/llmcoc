<script setup lang="ts">
import type { ScenarioHandout } from '@/api/sessionTypes'
import ScenarioChip from './ScenarioChip.vue'
import ScenarioSection from './ScenarioSection.vue'

defineProps<{ handouts: ScenarioHandout[] }>()
</script>

<template>
  <ScenarioSection title="预设身份卡 (HO)" note="（入房可选认领或随机抽签；秘密正文仅认领者与 KP 可见）">
    <ul class="space-y-2">
      <li v-for="(h, i) in handouts" :key="h.code || i" class="rounded-lg bg-surface-2 p-3">
        <div class="mb-1 flex items-center gap-2">
          <ScenarioChip>{{ h.code || `HO${i + 1}` }}</ScenarioChip>
          <span v-if="h.title" class="text-sm font-semibold text-fg">{{ h.title }}</span>
        </div>
        <p v-if="h.public_hook" class="text-sm leading-relaxed text-fg-muted">{{ h.public_hook }}</p>
        <p v-if="h.secret" class="mt-2 whitespace-pre-wrap rounded-md border border-info/30 bg-info/10 px-2.5 py-2 text-xs leading-relaxed text-fg-muted">
          <span class="font-medium text-info">隐藏秘密（管理员可见）：</span>{{ h.secret }}
        </p>
      </li>
    </ul>
  </ScenarioSection>
</template>
