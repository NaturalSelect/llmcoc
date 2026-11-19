<script setup lang="ts">
import { Coins } from '@lucide/vue'
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { AuctionBuyResult, AuctionListing, AuctionListingResult, AuctionSort } from '@/api/auctionTypes'
import type { InventoryItem } from '@/api/characterTypes'
import type { Page } from '@/api/pagination'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import FormField from '@/components/base/FormField.vue'
import PageHeader from '@/components/base/PageHeader.vue'
import PaginationBar from '@/components/base/PaginationBar.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import Skeleton from '@/components/base/Skeleton.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import TabBar from '@/components/base/TabBar.vue'
import { characterApi } from '@/components/character/characterApi'
import { ITEM_TYPE_LABEL, MANUAL_ITEM_TYPE_LABEL, itemLabel } from '@/components/character/characterLogic'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'
import { fmtDate } from '@/utils/format'
import { SORT_OPTIONS, browseQuery, buyBlocker, listingLabel, planListing, statusLabel, statusTone } from './auctionLogic'

const PAGE_SIZE = 12
const TABS = [
  { key: 'browse', label: '在售物品' },
  { key: 'mine', label: '我的挂单' },
]

const auth = useAuthStore()
const ui = useUiStore()

const tab = ref<'browse' | 'mine'>('browse')
const busy = ref(false)

const q = ref('')
const typeFilter = ref('')
const sort = ref<AuctionSort>('newest')
const listings = ref<AuctionListing[]>([])
const browsePage = ref(1)
const browseTotal = ref(0)
const browsePages = ref(1)
const browseLoading = ref(false)
let browseSeq = 0

const mine = ref<AuctionListing[]>([])
const minePage = ref(1)
const mineTotal = ref(0)
const minePages = ref(1)

const listingOpen = ref(false)
const warehouse = ref<InventoryItem[]>([])
const warehouseLoading = ref(false)
const pick = ref(0)
const countInput = ref<number | string>(1)
const priceInput = ref<number | string>('')

const picked = computed<InventoryItem | null>(() => warehouse.value[pick.value] ?? null)
const coins = computed(() => auth.user?.coins ?? 0)

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

function typeLabel(type: string): string {
  return ITEM_TYPE_LABEL[type as keyof typeof ITEM_TYPE_LABEL] ?? '杂物'
}

async function loadBrowse(page: number): Promise<void> {
  const next = Math.max(1, page)
  const seq = ++browseSeq
  browseLoading.value = true
  try {
    const resp = await api<Page<AuctionListing>>(
      'GET',
      `/api/auction/listings?${browseQuery({ q: q.value, type: typeFilter.value, sort: sort.value, page: next, pageSize: PAGE_SIZE })}`,
    )
    if (seq !== browseSeq) return
    // NOTE: 买下或下架最后一页的最后一条后，当前页会变空，退回上一页
    if ((resp?.items ?? []).length === 0 && next > 1) return await loadBrowse(next - 1)
    listings.value = resp?.items ?? []
    browsePage.value = Math.max(1, Number(resp?.page || next))
    browseTotal.value = Math.max(0, Number(resp?.total || 0))
    browsePages.value = Math.max(1, Number(resp?.total_pages || 1))
  } finally {
    if (seq === browseSeq) browseLoading.value = false
  }
}

async function loadMine(page: number): Promise<void> {
  const next = Math.max(1, page)
  const resp = await api<Page<AuctionListing>>('GET', `/api/auction/mine?page=${next}&page_size=${PAGE_SIZE}`)
  if ((resp?.items ?? []).length === 0 && next > 1) return loadMine(next - 1)
  mine.value = resp?.items ?? []
  minePage.value = Math.max(1, Number(resp?.page || next))
  mineTotal.value = Math.max(0, Number(resp?.total || 0))
  minePages.value = Math.max(1, Number(resp?.total_pages || 1))
}

async function refresh(): Promise<void> {
  try {
    await (tab.value === 'browse' ? loadBrowse(browsePage.value) : loadMine(minePage.value))
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

async function goBrowse(page: number): Promise<void> {
  try {
    await loadBrowse(page)
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

async function goMine(page: number): Promise<void> {
  try {
    await loadMine(page)
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

watch(tab, (t) => (t === 'browse' ? goBrowse(browsePage.value) : goMine(1)))
watch([typeFilter, sort], () => goBrowse(1))

async function buy(l: AuctionListing) {
  const blocked = buyBlocker(l, auth.user?.id, coins.value)
  if (blocked) {
    ui.toast(blocked, 'error')
    return
  }
  if (!(await ui.confirm(`确认花费 ${l.price} 金币购买「${listingLabel(l)}」？`, { confirmText: '购买' }))) return
  busy.value = true
  try {
    const r = await api<AuctionBuyResult>('POST', `/api/auction/listings/${l.id}/buy`)
    if (auth.user) auth.user.coins = r.coins
    ui.toast('购买成功，物品已放入仓库')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    busy.value = false
  }
  await refresh()
}

async function cancel(l: AuctionListing) {
  if (!(await ui.confirm(`确认下架「${listingLabel(l)}」？物品会退回仓库。`, { confirmText: '下架' }))) return
  busy.value = true
  try {
    await api<AuctionListingResult>('DELETE', `/api/auction/listings/${l.id}`)
    ui.toast('已下架，物品已退回仓库')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    busy.value = false
  }
  await refresh()
}

async function openListing() {
  pick.value = 0
  countInput.value = 1
  priceInput.value = ''
  warehouse.value = []
  listingOpen.value = true
  warehouseLoading.value = true
  try {
    warehouse.value = (await characterApi.warehouse())?.warehouse ?? []
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    warehouseLoading.value = false
  }
}

async function submitListing() {
  const item = picked.value
  const plan = planListing(item, countInput.value, priceInput.value)
  if (!plan.ok) {
    ui.toast(plan.error, 'error')
    return
  }
  if (!(await ui.confirm(plan.confirmMessage, { confirmText: '上架' }))) return
  busy.value = true
  try {
    await api<AuctionListingResult>('POST', '/api/auction/listings', {
      item: { name: item!.name, desc: item!.desc ?? '' },
      count: plan.count,
      price: plan.price,
    })
    listingOpen.value = false
    ui.toast('已上架')
    if (tab.value === 'mine') await goMine(1)
    else tab.value = 'mine'
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    busy.value = false
  }
}

onMounted(() => goBrowse(1))
</script>

<template>
  <div class="mx-auto w-full max-w-4xl space-y-5 p-5">
    <PageHeader title="拍卖行" subtitle="玩家之间一口价交易仓库里的物品">
      <template #actions>
        <span class="inline-flex items-center gap-1.5 rounded-full bg-surface-2 px-3 py-1.5 text-sm text-fg-muted">
          <Coins :size="16" :stroke-width="1.75" class="text-accent" />
          当前金币：<b class="font-semibold tabular-nums text-accent">{{ coins }}</b>
        </span>
        <BaseButton size="sm" @click="openListing">上架物品</BaseButton>
      </template>
    </PageHeader>

    <TabBar v-model="tab" :tabs="TABS" />

    <section v-if="tab === 'browse'" class="space-y-4">
      <form class="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_9rem_11rem_auto]" @submit.prevent="goBrowse(1)">
        <BaseInput v-model.trim="q" placeholder="搜索物品名" aria-label="搜索物品名" />
        <BaseSelect v-model="typeFilter" aria-label="物品类型">
          <option value="">全部类型</option>
          <option v-for="(label, key) in MANUAL_ITEM_TYPE_LABEL" :key="key" :value="key">{{ label }}</option>
        </BaseSelect>
        <BaseSelect v-model="sort" aria-label="排序">
          <option v-for="o in SORT_OPTIONS" :key="o.key" :value="o.key">{{ o.label }}</option>
        </BaseSelect>
        <BaseButton type="submit" variant="secondary">搜索</BaseButton>
      </form>

      <div v-if="browseLoading && listings.length === 0" class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-busy="true">
        <PaperCard v-for="i in 6" :key="i" class="space-y-2">
          <Skeleton class="h-5 w-12" />
          <Skeleton class="h-4 w-1/2" />
          <Skeleton class="h-3 w-full" />
          <Skeleton class="h-7 w-full" />
        </PaperCard>
      </div>

      <div v-else class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <PaperCard v-for="l in listings" :key="l.id" hover class="flex flex-col">
          <div class="mb-3 flex items-center gap-2">
            <StampBadge tone="ledger">{{ typeLabel(l.item_type) }}</StampBadge>
            <StampBadge v-if="l.seller_id === auth.user?.id" tone="seal">我的挂单</StampBadge>
          </div>
          <h3 class="mb-1 text-base font-semibold text-fg">
            {{ l.item_name }}<span v-if="l.count > 1" class="ml-1.5 text-sm font-normal tabular-nums text-fg-muted">x{{ l.count }}</span>
          </h3>
          <p class="mb-2 flex-1 text-sm leading-relaxed text-fg-muted">{{ l.item_desc }}</p>
          <p class="mb-3 text-xs text-fg-subtle">卖家：{{ l.seller_name || '未知' }} · {{ fmtDate(l.created_at) }}</p>
          <div class="flex items-center justify-between gap-3">
            <span class="inline-flex items-center gap-1.5 text-sm font-semibold tabular-nums text-accent">
              <Coins :size="16" :stroke-width="1.75" />
              {{ l.price }} 金币
            </span>
            <BaseButton v-if="l.seller_id === auth.user?.id" size="sm" variant="ghost" :disabled="busy" @click="cancel(l)">下架</BaseButton>
            <BaseButton
              v-else
              size="sm"
              :disabled="busy || buyBlocker(l, auth.user?.id, coins) !== null"
              :title="buyBlocker(l, auth.user?.id, coins) ?? undefined"
              @click="buy(l)"
            >
              购买
            </BaseButton>
          </div>
        </PaperCard>
        <EmptyState v-if="!browseLoading && listings.length === 0" class="col-span-full">暂无在售物品</EmptyState>
      </div>

      <div v-if="listings.length > 0" class="flex flex-col items-center">
        <p class="text-xs tabular-nums text-fg-subtle">共 {{ browseTotal }} 件</p>
        <PaginationBar :page="browsePage" :total-pages="browsePages" @update:page="goBrowse" />
      </div>
    </section>

    <section v-else class="space-y-3">
      <EmptyState v-if="mine.length === 0">你还没有上架过物品</EmptyState>
      <template v-else>
        <ul class="divide-y divide-line rounded-xl border border-line bg-surface">
          <li v-for="l in mine" :key="l.id" class="flex flex-wrap items-center gap-3 px-4 py-3 text-sm">
            <StampBadge :tone="statusTone(l.status)">{{ statusLabel(l.status) }}</StampBadge>
            <span class="min-w-0 flex-1 truncate text-fg">{{ listingLabel(l) }}</span>
            <span class="shrink-0 tabular-nums text-accent">{{ l.price }} 金币</span>
            <span class="w-36 shrink-0 text-right text-xs tabular-nums text-fg-subtle">{{ fmtDate(l.closed_at || l.created_at) }}</span>
            <BaseButton v-if="l.status === 'active'" size="sm" variant="ghost" :disabled="busy" @click="cancel(l)">下架</BaseButton>
          </li>
        </ul>
        <div class="flex flex-col items-center">
          <p class="text-xs tabular-nums text-fg-subtle">共 {{ mineTotal }} 条</p>
          <PaginationBar :page="minePage" :total-pages="minePages" @update:page="goMine" />
        </div>
      </template>
    </section>

    <BaseModal :open="listingOpen" title="上架物品" @close="listingOpen = false">
      <p v-if="warehouseLoading" class="text-sm text-fg-muted">正在读取仓库……</p>
      <EmptyState v-else-if="warehouse.length === 0">仓库里没有物品。先在首页把人物卡的物品存入仓库，再来上架。</EmptyState>
      <div v-else class="space-y-4">
        <FormField label="物品">
          <BaseSelect v-model="pick">
            <option v-for="(it, i) in warehouse" :key="i" :value="i">{{ itemLabel(it) }} · {{ typeLabel(it.type) }}</option>
          </BaseSelect>
        </FormField>
        <FormField label="数量" :hint="picked ? `仓库里有 ${picked.count} 件` : undefined">
          <BaseInput v-model.number="countInput" type="number" min="1" />
        </FormField>
        <FormField label="总价（金币）" hint="按这个数量整单出售，买家不能只买一部分">
          <BaseInput v-model.number="priceInput" type="number" min="1" placeholder="例如 100" />
        </FormField>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <BaseButton variant="ghost" @click="listingOpen = false">取消</BaseButton>
          <BaseButton :loading="busy" :disabled="warehouseLoading || warehouse.length === 0" @click="submitListing">上架</BaseButton>
        </div>
      </template>
    </BaseModal>
  </div>
</template>
