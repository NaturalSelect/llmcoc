<script setup lang="ts">
import { onMounted } from 'vue'
import type { AdminShopItem } from '@/api/adminTypesContent'
import { api } from '@/api/client'
import { errorMessage, usePagedList } from '@/components/admin/scenarios/pagedList'
import { useToastRun } from '@/components/admin/scenarios/useToastRun'
import ShopCreateCard from '@/components/admin/shop/ShopCreateCard.vue'
import ShopItemList from '@/components/admin/shop/ShopItemList.vue'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
const run = useToastRun()
// NOTE: 管理端复用公开的商品列表接口（只返回已上架商品，按价格升序）
const list = usePagedList<AdminShopItem>('/api/shop/items')

onMounted(() => run(() => list.load()))

async function remove(item: AdminShopItem) {
  if (!item?.id) return
  const ok = await ui.confirm('确认删除商品“' + item.name + '”？删除后将从商城下架。', { danger: true, confirmText: '删除' })
  if (!ok) return
  try {
    await api('DELETE', '/api/admin/shop/items/' + item.id)
    ui.toast('商品已删除')
    await list.reloadAfterDelete()
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  }
}
</script>

<template>
  <div class="space-y-5">
    <ShopCreateCard :created="() => list.load(1)" />
    <ShopItemList
      :items="list.items.value"
      :page="list.page.value"
      :total="list.total.value"
      :total-pages="list.totalPages.value"
      @prev="run(list.prev)"
      @next="run(list.next)"
      @refresh="run(() => list.load(list.page.value))"
      @delete="remove"
    />
  </div>
</template>
