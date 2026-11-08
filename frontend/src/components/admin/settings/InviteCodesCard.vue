<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import type { InviteCode } from '@/api/adminTypesContent'
import { api } from '@/api/client'
import PagerHeader from '@/components/admin/scenarios/PagerHeader.vue'
import { errorMessage, usePagedList } from '@/components/admin/scenarios/pagedList'
import { useToastRun } from '@/components/admin/scenarios/useToastRun'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
const run = useToastRun()
const list = usePagedList<InviteCode>('/api/admin/invite-codes')
// NOTE: 数量输入框清空后是空字符串，提交时沿用旧逻辑回落到 5
const count = ref<number | string | null | undefined>(5)

onMounted(() => run(() => list.load()))

async function generate() {
  try {
    await api('POST', '/api/admin/invite-codes', { count: count.value || 5 })
    ui.toast('邀请码已生成')
    await list.load(1)
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  }
}

async function remove(id: number) {
  if (!(await ui.confirm('确认删除该邀请码？', { danger: true, confirmText: '删除' }))) return
  try {
    await api('DELETE', '/api/admin/invite-codes/' + id)
    ui.toast('已删除')
    await list.reloadAfterDelete()
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  }
}
</script>

<template>
  <PaperCard>
    <PagerHeader
      title="邀请码管理"
      unit="个"
      :page="list.page.value"
      :total-pages="list.totalPages.value"
      :total="list.total.value"
      @prev="run(list.prev)"
      @next="run(list.next)"
      @refresh="run(() => list.load(list.page.value))"
    >
      <template #before>
        <div class="w-20">
          <BaseInput
            v-model.number="count"
            type="number"
            min="1"
            max="100"
            placeholder="数量"
            aria-label="生成数量"
            class="text-center tabular-nums"
          />
        </div>
        <BaseButton size="sm" @click="generate">批量生成</BaseButton>
      </template>
    </PagerHeader>

    <EmptyState v-if="list.items.value.length === 0">暂无邀请码</EmptyState>
    <ul v-else class="space-y-2">
      <li v-for="ic in list.items.value" :key="ic.id" class="flex items-center justify-between gap-3 rounded-lg bg-surface-2 px-3 py-2">
        <div class="flex min-w-0 flex-wrap items-center gap-3">
          <code class="font-mono text-sm tracking-wider text-fg">{{ ic.code }}</code>
          <StampBadge v-if="!ic.used_by" tone="moss">未使用</StampBadge>
          <span v-else class="text-xs text-fg-subtle">已使用 · {{ ic.used_user?.username || '用户#' + ic.used_by }}</span>
        </div>
        <div class="flex shrink-0 items-center gap-3">
          <span class="text-xs tabular-nums text-fg-subtle">{{ new Date(ic.created_at).toLocaleDateString() }}</span>
          <BaseButton v-if="!ic.used_by" variant="danger" size="sm" @click="remove(ic.id)">
            <Trash2 :size="16" :stroke-width="1.75" />
            删除
          </BaseButton>
        </div>
      </li>
    </ul>
  </PaperCard>
</template>
