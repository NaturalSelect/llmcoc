<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { Page } from '@/api/pagination'
import type { PurchaseResult, ShopItem, ShopTransaction } from '@/api/shopTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import FormField from '@/components/base/FormField.vue'
import PaginationBar from '@/components/base/PaginationBar.vue'
import PaperCard from '@/components/base/PaperCard.vue'
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
  <div class="mx-auto w-full max-w-4xl p-5">
    <header class="mb-5 flex items-end justify-between border-b border-paper-edge pb-2">
      <h2 class="font-serif text-xl font-bold text-ink">商城</h2>
      <span class="font-type text-sm text-ink-soft">当前金币：<b class="text-seal">{{ auth.user?.coins ?? 0 }}</b></span>
    </header>

    <PaperCard class="mb-4">
      <FormField label="装备/武器/配件/复活 目标人物卡">
        <BaseSelect v-model="targetId">
          <option value="">请选择人物卡（金币与卡槽商品不需要）</option>
          <option v-for="c in chars.characters" :key="c.id" :value="c.id">
            {{ c.name }} · {{ c.occupation || '未知职业' }}{{ c.wound_state === 'dead' ? ' 【已死亡】' : '' }}
          </option>
        </BaseSelect>
      </FormField>
    </PaperCard>

    <div v-if="loading && items.length === 0" class="mb-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-busy="true">
      <PaperCard v-for="i in 6" :key="i" class="space-y-2">
        <Skeleton class="h-5 w-12" />
        <Skeleton class="h-4 w-1/2" />
        <Skeleton class="h-3 w-full" />
        <Skeleton class="h-3 w-4/5" />
        <Skeleton class="h-7 w-full" />
      </PaperCard>
    </div>

    <div v-else class="mb-4 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
      <PaperCard v-for="item in items" :key="item.id" hover class="flex flex-col">
        <div class="mb-3"><StampBadge tone="ledger">{{ itemTypeLabel(item.item_type) }}</StampBadge></div>
        <h3 class="mb-1 font-serif font-bold text-ink">{{ item.name }}</h3>
        <p class="mb-4 flex-1 text-xs leading-relaxed text-ink-soft">{{ item.description }}</p>
        <div class="flex items-center justify-between">
          <span class="font-type font-bold text-seal">{{ item.price }} 金币</span>
          <BaseButton :disabled="(auth.user?.coins ?? 0) < item.price || purchasing" @click="purchase(item)">购买</BaseButton>
        </div>
      </PaperCard>
      <p v-if="!loading && items.length === 0" class="col-span-full py-8 text-center text-sm text-ink-faint">暂无商品</p>
    </div>

    <div v-if="items.length > 0" class="mb-8 text-center font-type text-xs text-ink-faint">
      共 {{ itemTotal }} 个
      <PaginationBar :page="itemPage" :total-pages="itemPages" @update:page="changeItemPage" />
    </div>

    <section>
      <h3 class="mb-3 font-serif text-base font-bold text-ink">购买记录</h3>
      <ul class="space-y-1.5">
        <li
          v-for="t in transactions"
          :key="t.id"
          class="flex items-center justify-between rounded-sm border border-paper-edge bg-paper px-3 py-2 font-type text-xs"
        >
          <span class="text-ink">{{ t.shop_item?.name }}</span>
          <span class="text-seal">-{{ t.coins_spent }} 金币</span>
          <span class="text-ink-faint">{{ fmtDate(t.created_at) }}</span>
        </li>
      </ul>
      <p v-if="transactions.length === 0" class="text-xs text-ink-faint">暂无购买记录</p>
      <div v-else class="mt-2 text-center font-type text-xs text-ink-faint">
        共 {{ txTotal }} 条
        <PaginationBar :page="txPage" :total-pages="txPages" @update:page="changeTxPage" />
      </div>
    </section>
  </div>
</template>
