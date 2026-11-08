<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { AdminUser } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import PaperCard from '@/components/base/PaperCard.vue'
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

    <PaperCard tag="section" aria-labelledby="user-list-title">
      <div class="mb-4 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div>
          <h3 id="user-list-title" class="font-type text-sm font-bold tracking-widest text-seal">用户列表</h3>
          <p class="mt-1 font-type text-xs text-ink-faint">共 {{ total }} 个 · 第 {{ page }} / {{ totalPages }} 页</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <label for="admin-user-page-size" class="font-type text-xs text-ink-soft">每页</label>
          <BaseSelect id="admin-user-page-size" :model-value="pageSize" class="!w-20 !py-1 !text-xs" @update:model-value="changePageSize">
            <option v-for="n in PAGE_SIZE_OPTIONS" :key="n" :value="n">{{ n }}</option>
          </BaseSelect>
          <BaseButton variant="ghost" class="!px-3 !py-1 !text-xs" :disabled="page <= 1" @click="goPage(page - 1)">上一页</BaseButton>
          <BaseButton variant="ghost" class="!px-3 !py-1 !text-xs" :disabled="page >= totalPages" @click="goPage(page + 1)">下一页</BaseButton>
          <BaseButton variant="ghost" class="!px-3 !py-1 !text-xs" @click="safeLoad(page)">刷新</BaseButton>
        </div>
      </div>

      <ul class="space-y-2">
        <li
          v-for="u in users"
          :key="u.id"
          class="flex flex-wrap items-center justify-between gap-2 border-b border-paper-edge/70 pb-2 text-xs"
        >
          <div class="min-w-0 flex-1">
            <span class="font-bold text-ink">#{{ u.id }} {{ u.username }}</span>
            <span class="ml-1.5 inline-block max-w-[16rem] truncate align-bottom text-ink-faint">{{ u.email }}</span>
          </div>
          <div class="flex shrink-0 flex-wrap items-center gap-2">
            <span class="font-type text-ink-soft">💰{{ u.coins }}</span>
            <StampBadge :tone="u.role === 'admin' ? 'seal' : 'faint'">{{ u.role }}</StampBadge>
            <StampBadge v-if="u.is_banned" tone="seal">已封</StampBadge>
            <button type="button" class="text-ink-faint transition hover:text-seal" @click="toggleAdmin(u)">切换权限</button>
            <button
              type="button"
              class="transition"
              :class="u.is_banned ? 'text-moss hover:text-ink' : 'text-seal/70 hover:text-seal'"
              @click="toggleBan(u)"
            >
              {{ u.is_banned ? '解封' : '封号' }}
            </button>
            <button type="button" class="text-ink-faint transition hover:text-seal" @click="inventoryUser = { id: u.id, username: u.username }">
              背包
            </button>
          </div>
        </li>
        <li v-if="users.length === 0" class="py-4 text-center text-ink-faint">暂无用户</li>
      </ul>
    </PaperCard>

    <AdminInventoryModal :user="inventoryUser" @close="inventoryUser = null" />
  </div>
</template>
