<script setup lang="ts">
import { ChevronLeft, ChevronRight, Coins, RefreshCw } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import type { AdminUser } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import AdminInventoryModal from '@/components/admin/users/AdminInventoryModal.vue'
import RechargeCard from '@/components/admin/users/RechargeCard.vue'
import { errMsg } from '@/components/admin/shared/errMsg'
import { PAGE_SIZE_OPTIONS, clampPage, normalizeItemsPage, sanitizePageSize } from '@/components/admin/shared/paging'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()

const users = ref<AdminUser[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const totalPages = ref(1)
const inventoryUser = ref<{ id: number; username: string } | null>(null)
// NOTE: 快速连点翻页时只采纳最后一次请求的结果
let loadSeq = 0

async function loadUsers(p?: number) {
  const nextPage = Math.max(1, Number(p || page.value || 1))
  const size = Math.max(1, Number(pageSize.value || 20))
  const seq = ++loadSeq
  const resp = await api('GET', `/api/admin/users?page=${nextPage}&page_size=${size}`)
  if (seq !== loadSeq) return
  const s = normalizeItemsPage<AdminUser>(resp, nextPage, size)
  users.value = s.items
  page.value = s.page
  pageSize.value = s.pageSize
  total.value = s.total
  totalPages.value = s.totalPages
}

async function safeLoad(p?: number) {
  try {
    await loadUsers(p)
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

const goPage = (p: number) => safeLoad(clampPage(p, totalPages.value))

// NOTE: 直接用事件携带的新值，不依赖 v-model 与 change 监听器的触发先后；换每页条数后回到第 1 页
function changePageSize(size: unknown) {
  pageSize.value = sanitizePageSize(size)
  return safeLoad(1)
}

async function toggleAdmin(u: AdminUser) {
  const newRole = u.role === 'admin' ? 'user' : 'admin'
  if (!(await ui.confirm(`将 ${u.username} 设为 ${newRole}？`))) return
  try {
    await api('PUT', `/api/admin/users/${u.id}/role`, { role: newRole })
    u.role = newRole
    ui.toast('角色已更新')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

async function toggleBan(u: AdminUser) {
  if (u.is_banned) {
    if (!(await ui.confirm(`解封 ${u.username}？`, { confirmText: '解封' }))) return
    try {
      await api('PUT', `/api/admin/users/${u.id}/unban`)
      u.is_banned = false
      u.ban_reason = ''
      ui.toast('已解封')
    } catch (e) {
      ui.toast(errMsg(e), 'error')
    }
    return
  }
  const reason = await ui.confirm(`封号 ${u.username}？`, {
    withInput: true,
    inputPlaceholder: '封号原因（可留空）',
    danger: true,
    confirmText: '封号',
  })
  if (reason === null) return
  try {
    await api('PUT', `/api/admin/users/${u.id}/ban`, { reason })
    u.is_banned = true
    u.ban_reason = reason
    ui.toast('已封号')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

onMounted(() => safeLoad())
</script>

<template>
  <div class="grid grid-cols-1 gap-5 lg:grid-cols-[22rem_minmax(0,1fr)]">
    <RechargeCard @recharged="safeLoad()" />

    <PaperCard tag="section" aria-labelledby="user-list-title" class="min-w-0">
      <div class="mb-4 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div>
          <SectionHeading id="user-list-title">用户列表</SectionHeading>
          <p class="mt-1 text-xs tabular-nums text-fg-subtle">共 {{ total }} 个 · 第 {{ page }} / {{ totalPages }} 页</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <label for="admin-user-page-size" class="text-xs text-fg-muted">每页</label>
          <!-- NOTE: BaseSelect 没有 size 属性，用外层容器把高度压到与 sm 按钮一致（h-8） -->
          <div class="w-20 [&_select]:h-8">
            <BaseSelect id="admin-user-page-size" :model-value="pageSize" @update:model-value="changePageSize">
              <option v-for="n in PAGE_SIZE_OPTIONS" :key="n" :value="n">{{ n }}</option>
            </BaseSelect>
          </div>
          <BaseButton variant="ghost" size="sm" :disabled="page <= 1" @click="goPage(page - 1)">
            <ChevronLeft :size="16" :stroke-width="1.75" />上一页
          </BaseButton>
          <BaseButton variant="ghost" size="sm" :disabled="page >= totalPages" @click="goPage(page + 1)">
            下一页<ChevronRight :size="16" :stroke-width="1.75" />
          </BaseButton>
          <BaseButton variant="ghost" size="sm" @click="safeLoad(page)">
            <RefreshCw :size="16" :stroke-width="1.75" />刷新
          </BaseButton>
        </div>
      </div>

      <EmptyState v-if="users.length === 0">暂无用户</EmptyState>
      <template v-else>
        <div class="hidden gap-3 px-1 pb-2 text-xs text-fg-subtle md:grid md:grid-cols-[minmax(0,1fr)_4.5rem_8rem_16rem]">
          <span>用户</span>
          <span>金币</span>
          <span>角色</span>
          <span class="text-right">操作</span>
        </div>
        <ul class="divide-y divide-line border-t border-line">
          <li
            v-for="u in users"
            :key="u.id"
            class="flex flex-wrap items-center gap-x-3 gap-y-2 px-1 py-3 text-sm md:grid md:grid-cols-[minmax(0,1fr)_4.5rem_8rem_16rem]"
          >
            <div class="min-w-0 flex-1 md:flex-none">
              <div class="truncate font-medium text-fg">#{{ u.id }} {{ u.username }}</div>
              <div class="truncate text-xs text-fg-subtle">{{ u.email }}</div>
            </div>
            <span class="inline-flex items-center gap-1 tabular-nums text-fg-muted">
              <Coins :size="16" :stroke-width="1.75" class="text-accent" />{{ u.coins }}
            </span>
            <div class="flex flex-wrap items-center gap-1.5">
              <StampBadge :tone="u.role === 'admin' ? 'seal' : 'faint'">{{ u.role }}</StampBadge>
              <StampBadge v-if="u.is_banned" tone="danger">已封</StampBadge>
            </div>
            <div class="flex flex-wrap items-center gap-2 md:justify-end">
              <BaseButton variant="ghost" size="sm" @click="toggleAdmin(u)">切换权限</BaseButton>
              <BaseButton :variant="u.is_banned ? 'success' : 'danger'" size="sm" @click="toggleBan(u)">
                {{ u.is_banned ? '解封' : '封号' }}
              </BaseButton>
              <BaseButton variant="ghost" size="sm" @click="inventoryUser = { id: u.id, username: u.username }">背包</BaseButton>
            </div>
          </li>
        </ul>
      </template>
    </PaperCard>

    <AdminInventoryModal :user="inventoryUser" @close="inventoryUser = null" />
  </div>
</template>
