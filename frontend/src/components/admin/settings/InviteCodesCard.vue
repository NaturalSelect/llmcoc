<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { InviteCode } from '@/api/adminTypesContent'
import { api } from '@/api/client'
import PagerHeader from '@/components/admin/scenarios/PagerHeader.vue'
import { errorMessage, usePagedList } from '@/components/admin/scenarios/pagedList'
import { useToastRun } from '@/components/admin/scenarios/useToastRun'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import PaperCard from '@/components/base/PaperCard.vue'
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
        <BaseInput
          v-model.number="count"
          type="number"
          min="1"
          max="100"
          placeholder="数量"
          aria-label="生成数量"
          class="!w-20 !px-2 !py-1 text-center !text-xs"
        />
        <BaseButton class="!px-2.5 !py-1 !text-xs" @click="generate">批量生成</BaseButton>
      </template>
    </PagerHeader>

    <p v-if="list.items.value.length === 0" class="text-xs text-ink-faint">暂无邀请码</p>
    <ul class="space-y-2">
      <li
        v-for="ic in list.items.value"
        :key="ic.id"
        class="flex items-center justify-between border-b border-paper-edge pb-2 text-xs"
      >
        <div class="flex items-center gap-3">
          <code class="font-type tracking-wider text-ink">{{ ic.code }}</code>
          <span v-if="!ic.used_by" class="text-moss">未使用</span>
          <span v-else class="text-ink-faint">已使用 · {{ ic.used_user?.username || '用户#' + ic.used_by }}</span>
        </div>
        <div class="flex items-center gap-2">
          <span class="text-ink-faint">{{ new Date(ic.created_at).toLocaleDateString() }}</span>
          <button v-if="!ic.used_by" type="button" class="text-seal transition hover:underline" @click="remove(ic.id)">删除</button>
        </div>
      </li>
    </ul>
  </PaperCard>
</template>
