<script setup lang="ts">
import { Coins } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { Page } from '@/api/pagination'
import type { PurchaseResult, ShopItem, ShopTransaction } from '@/api/shopTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import FormField from '@/components/base/FormField.vue'
import PageHeader from '@/components/base/PageHeader.vue'
import PaginationBar from '@/components/base/PaginationBar.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import Skeleton from '@/components/base/Skeleton.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { useAuthStore } from '@/stores/auth'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'
import { fmtDate } from '@/utils/format'
import { isReviveItem, itemTypeLabel, planPurchase } from './shopLogic'

const PAGE_SIZE = 20

const auth = useAuthStore()
const chars = useCharactersStore()
const ui = useUiStore()

const loading = ref(false)
const purchasing = ref(false)
const targetId = ref<number | ''>('')

const items = ref<ShopItem[]>([])
const itemPage = ref(1)
const itemTotal = ref(0)
const itemPages = ref(1)

const transactions = ref<ShopTransaction[]>([])
const txPage = ref(1)
const txTotal = ref(0)
const txPages = ref(1)

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

async function loadItems(page: number) {
  const next = Math.max(1, page)
  const resp = await api<Page<ShopItem>>('GET', `/api/shop/items?page=${next}&page_size=${PAGE_SIZE}`)
  items.value = resp?.items || []
  itemPage.value = Math.max(1, Number(resp?.page || next))
  itemTotal.value = Math.max(0, Number(resp?.total || 0))
  itemPages.value = Math.max(1, Number(resp?.total_pages || 1))
}

async function loadTransactions(page: number) {
  const next = Math.max(1, page)
  const resp = await api<Page<ShopTransaction>>('GET', `/api/shop/transactions?page=${next}&page_size=${PAGE_SIZE}`)
  transactions.value = resp?.items || []
  txPage.value = Math.max(1, Number(resp?.page || next))
  txTotal.value = Math.max(0, Number(resp?.total || 0))
  txPages.value = Math.max(1, Number(resp?.total_pages || 1))
}

async function changeItemPage(p: number) {
  try {
    await loadItems(p)
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

async function changeTxPage(p: number) {
  try {
    await loadTransactions(p)
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

async function purchase(item: ShopItem) {
  const plan = planPurchase(item, targetId.value, chars.characters)
  if (!plan.ok) {
    ui.toast(plan.error, 'error')
    return
  }
  if (!(await ui.confirm(plan.confirmMessage, { confirmText: plan.confirmText }))) return
  purchasing.value = true
  try {
    const body: { item_id: number; character_card_id?: number } = { item_id: item.id }
    if (plan.characterId != null) body.character_card_id = plan.characterId
    const r = await api<PurchaseResult>('POST', '/api/shop/purchase', body)
    if (auth.user) {
      auth.user.coins = r.coins
      auth.user.card_slots = r.card_slots
    }
    if (r.character_card) chars.syncCharacter(r.character_card)
    await loadTransactions(1)
    ui.toast(isReviveItem(item.item_type) ? '复活成功！调查员已苏醒，但部分记忆与装备已消散……' : '购买成功！')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    purchasing.value = false
  }
}

onMounted(async () => {
  loading.value = true
  try {
    await Promise.all([loadItems(1), loadTransactions(1), chars.loadCharacters()])
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="mx-auto w-full max-w-4xl space-y-5 p-5">
    <PageHeader title="商城">
      <template #actions>
        <span class="inline-flex items-center gap-1.5 rounded-full bg-surface-2 px-3 py-1.5 text-sm text-fg-muted">
          <Coins :size="16" :stroke-width="1.75" class="text-accent" />
          当前金币：<b class="font-semibold tabular-nums text-accent">{{ auth.user?.coins ?? 0 }}</b>
        </span>
      </template>
    </PageHeader>

    <PaperCard>
      <FormField label="装备/武器/配件/复活 目标人物卡">
        <BaseSelect v-model="targetId">
          <option value="">请选择人物卡（金币与卡槽商品不需要）</option>
          <option v-for="c in chars.characters" :key="c.id" :value="c.id">
            {{ c.name }} · {{ c.occupation || '未知职业' }}{{ c.wound_state === 'dead' ? ' 【已死亡】' : '' }}
          </option>
        </BaseSelect>
      </FormField>
    </PaperCard>

    <div v-if="loading && items.length === 0" class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-busy="true">
      <PaperCard v-for="i in 6" :key="i" class="space-y-2">
        <Skeleton class="h-5 w-12" />
        <Skeleton class="h-4 w-1/2" />
        <Skeleton class="h-3 w-full" />
        <Skeleton class="h-3 w-4/5" />
        <Skeleton class="h-7 w-full" />
      </PaperCard>
    </div>

    <div v-else class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
      <PaperCard v-for="item in items" :key="item.id" hover class="flex flex-col">
        <div class="mb-3"><StampBadge tone="ledger">{{ itemTypeLabel(item.item_type) }}</StampBadge></div>
        <h3 class="mb-1 text-base font-semibold text-fg">{{ item.name }}</h3>
        <p class="mb-4 flex-1 text-sm leading-relaxed text-fg-muted">{{ item.description }}</p>
        <div class="flex items-center justify-between gap-3">
          <span class="inline-flex items-center gap-1.5 text-sm font-semibold tabular-nums text-accent">
            <Coins :size="16" :stroke-width="1.75" />
            {{ item.price }} 金币
          </span>
          <BaseButton size="sm" :disabled="(auth.user?.coins ?? 0) < item.price || purchasing" @click="purchase(item)">购买</BaseButton>
        </div>
      </PaperCard>
      <EmptyState v-if="!loading && items.length === 0" class="col-span-full">暂无商品</EmptyState>
    </div>

    <div v-if="items.length > 0" class="flex flex-col items-center">
      <p class="text-xs tabular-nums text-fg-subtle">共 {{ itemTotal }} 个</p>
      <PaginationBar :page="itemPage" :total-pages="itemPages" @update:page="changeItemPage" />
    </div>

    <section class="space-y-3">
      <SectionHeading>购买记录</SectionHeading>
      <EmptyState v-if="transactions.length === 0">暂无购买记录</EmptyState>
      <template v-else>
        <ul class="divide-y divide-line rounded-xl border border-line bg-surface">
          <li v-for="t in transactions" :key="t.id" class="flex items-center justify-between gap-3 px-4 py-3 text-sm">
            <span class="min-w-0 flex-1 truncate text-fg">{{ t.shop_item?.name }}</span>
            <span class="shrink-0 tabular-nums text-accent">-{{ t.coins_spent }} 金币</span>
            <span class="w-36 shrink-0 text-right text-xs tabular-nums text-fg-subtle">{{ fmtDate(t.created_at) }}</span>
          </li>
        </ul>
        <div class="flex flex-col items-center">
          <p class="text-xs tabular-nums text-fg-subtle">共 {{ txTotal }} 条</p>
          <PaginationBar :page="txPage" :total-pages="txPages" @update:page="changeTxPage" />
        </div>
      </template>
    </section>
  </div>
</template>
