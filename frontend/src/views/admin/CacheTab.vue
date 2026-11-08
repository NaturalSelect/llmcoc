<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { CacheEntry, CacheStats } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import CacheEntryDetail from '@/components/admin/cache/CacheEntryDetail.vue'
import CacheKeysCard from '@/components/admin/cache/CacheKeysCard.vue'
import CacheStatsCard from '@/components/admin/cache/CacheStatsCard.vue'
import LlmStatsCard from '@/components/admin/cache/LlmStatsCard.vue'
import SessionContextCard from '@/components/admin/cache/SessionContextCard.vue'
import { errMsg } from '@/components/admin/shared/errMsg'
import { clampPage, normalizeKeysPage } from '@/components/admin/shared/paging'
import { useUiStore } from '@/stores/ui'

// NOTE: 旧前端的规则缓存页没有任何定时刷新（admin.js / app.js 里没有 setInterval），只在进入页签和点击按钮时拉取，这里同样不加轮询
const KEY_PAGE_SIZE = 20

const ui = useUiStore()

const stats = ref<CacheStats | null>(null)
const keys = ref<string[]>([])
const keyPage = ref(1)
const keyTotal = ref(0)
const keyTotalPages = ref(1)
const entry = ref<CacheEntry | null>(null)
const entryLoading = ref(false)
// NOTE: 只采纳最后一次发起的请求结果，避免快速翻页/连点「查看」时旧响应覆盖新状态
let keysSeq = 0
let entrySeq = 0

async function loadStats() {
  try {
    stats.value = await api<CacheStats>('GET', '/api/admin/cache/stats')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

async function loadKeys(page?: number) {
  const next = Math.max(1, Number(page || keyPage.value || 1))
  const seq = ++keysSeq
  try {
    const resp = await api('GET', `/api/admin/cache/keys?page=${next}&page_size=${KEY_PAGE_SIZE}`)
    if (seq !== keysSeq) return
    const s = normalizeKeysPage(resp, next, KEY_PAGE_SIZE)
    keys.value = s.items
    keyPage.value = s.page
    keyTotal.value = s.total
    keyTotalPages.value = s.totalPages
  } catch (e) {
    if (seq === keysSeq) ui.toast(errMsg(e), 'error')
  }
}

const goKeyPage = (p: number) => loadKeys(clampPage(p, keyTotalPages.value))

function closeEntry() {
  entrySeq += 1
  entry.value = null
  entryLoading.value = false
}

async function viewEntry(key: string) {
  const seq = ++entrySeq
  entryLoading.value = true
  entry.value = { key, value: '' }
  try {
    const resp = await api<CacheEntry>('GET', '/api/admin/cache/entry?key=' + encodeURIComponent(key))
    if (seq === entrySeq) entry.value = resp
  } catch (e) {
    if (seq === entrySeq) {
      entry.value = null
      ui.toast('获取缓存详情失败：' + errMsg(e), 'error')
    }
  } finally {
    if (seq === entrySeq) entryLoading.value = false
  }
}

async function clearCache() {
  if (!(await ui.confirm('确认清空所有规则缓存？统计计数也将重置。', { danger: true, confirmText: '清空' }))) return
  try {
    await api('DELETE', '/api/admin/cache')
    closeEntry()
    ui.toast('缓存已清空')
    await Promise.all([loadStats(), loadKeys()])
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

async function deleteEntry(key: string) {
  if (!(await ui.confirm('确认删除缓存条目：' + key + '？', { danger: true, confirmText: '删除' }))) return
  try {
    await api('DELETE', '/api/admin/cache/entry?key=' + encodeURIComponent(key))
    if (entry.value?.key === key) closeEntry()
    ui.toast('条目已删除')
    // NOTE: 若当前页只剩 1 条且不是第 1 页，回退一页，否则会停在空页
    const target = keys.value.length <= 1 && keyPage.value > 1 ? keyPage.value - 1 : undefined
    await Promise.all([loadStats(), loadKeys(target)])
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

onMounted(() => {
  void loadStats()
  void loadKeys()
})
</script>

<template>
  <div class="space-y-5">
    <CacheStatsCard :stats="stats" @refresh="loadStats" @clear="clearCache" />
    <LlmStatsCard />
    <SessionContextCard />
    <CacheKeysCard
      :keys="keys"
      :total="keyTotal"
      :page="keyPage"
      :total-pages="keyTotalPages"
      @refresh="loadKeys()"
      @prev="goKeyPage(keyPage - 1)"
      @next="goKeyPage(keyPage + 1)"
      @view="viewEntry"
      @remove="deleteEntry"
    />
    <CacheEntryDetail v-if="entryLoading || entry" :loading="entryLoading" :entry="entry" @close="closeEntry" />
  </div>
</template>
