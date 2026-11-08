<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { AdminCharacterCard } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import { fetchAllPages } from '@/api/pagination'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import FormField from '@/components/base/FormField.vue'
import { useUiStore } from '@/stores/ui'
import { errMsg } from '../shared/errMsg'

// NOTE: 只从「用户」tab 的背包按钮打开，所以放在 users/ 下；user 非空即打开
const props = defineProps<{ user: { id: number; username: string } | null }>()
const emit = defineEmits<{ close: [] }>()
const ui = useUiStore()

const cards = ref<AdminCharacterCard[]>([])
const selectedId = ref<number | ''>('')
const input = ref('')
const loading = ref(false)
// NOTE: 目标用户切换或弹窗关闭后，迟到的旧请求结果必须丢弃
let loadToken = 0

const selected = computed(() => cards.value.find((c) => c.id === selectedId.value) ?? null)
const inventory = computed(() => selected.value?.inventory ?? [])
const title = computed(() => (props.user ? `为 ${props.user.username} 的角色卡添加物品` : ''))

async function loadCards(userId: number, token: number) {
  loading.value = true
  try {
    const all = await fetchAllPages<AdminCharacterCard>(`/api/admin/users/${userId}/characters`, {
      onProgress: (items) => {
        if (token === loadToken) cards.value = items
      },
    })
    if (token === loadToken) cards.value = all
  } catch (e) {
    if (token === loadToken) {
      ui.toast(errMsg(e), 'error')
      cards.value = []
    }
  } finally {
    if (token === loadToken) loading.value = false
  }
}

watch(
  () => props.user,
  (u) => {
    const token = ++loadToken
    cards.value = []
    selectedId.value = ''
    input.value = ''
    loading.value = false
    if (u) void loadCards(u.id, token)
  },
  { immediate: true },
)

// NOTE: 换卡时清空物品输入框，避免把上一张卡的草稿误加到新卡
watch(selectedId, () => {
  input.value = ''
})

async function submit() {
  const card = selected.value
  if (!card) {
    ui.toast('请先选择角色卡', 'error')
    return
  }
  const item = input.value.trim()
  if (!item) {
    ui.toast('物品名不能为空', 'error')
    return
  }
  const token = loadToken
  loading.value = true
  try {
    const updated = await api<AdminCharacterCard>('POST', `/api/characters/${card.id}/inventory`, { item })
    if (token !== loadToken) return
    // NOTE: 用返回的最新卡片替换列表中的对应项，背包预览随之刷新
    cards.value = cards.value.map((c) => (c.id === updated.id ? updated : c))
    input.value = ''
    ui.toast('物品已添加')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    if (token === loadToken) loading.value = false
  }
}
</script>

<template>
  <BaseModal :open="user !== null" :title="title" max-width="max-w-md" @close="emit('close')">
    <div class="space-y-3">
      <p v-if="loading && !cards.length" class="text-sm text-fg-subtle" role="status">加载角色卡中…</p>
      <p v-else-if="!loading && !cards.length" class="text-sm text-fg-subtle">该用户暂无角色卡。</p>

      <FormField v-if="cards.length" label="选择角色卡">
        <BaseSelect v-model="selectedId">
          <option value="">— 请选择 —</option>
          <option v-for="c in cards" :key="c.id" :value="c.id">{{ c.name + (c.occupation ? '（' + c.occupation + '）' : '') }}</option>
        </BaseSelect>
      </FormField>

      <div v-if="selected">
        <span class="mb-1.5 block text-sm text-fg-muted">当前背包</span>
        <ul v-if="inventory.length" class="flex flex-wrap gap-1.5">
          <li v-for="(item, idx) in inventory" :key="idx" class="rounded-lg bg-surface-2 px-2.5 py-1 text-xs text-fg">
            {{ item }}
          </li>
        </ul>
        <p v-else class="text-sm text-fg-subtle">—（空）</p>
      </div>

      <FormField v-if="selected" label="新增物品" hint="可按「名称(描述, x数量)」约定填写；整串会作为一件物品写入背包。">
        <BaseInput v-model="input" placeholder="例如：绷带(x3) 或 左轮手枪(.38, x1)" @keydown.enter="submit" />
      </FormField>

      <div class="flex gap-2 pt-1">
        <BaseButton variant="ghost" class="flex-1" @click="emit('close')">关闭</BaseButton>
        <BaseButton class="flex-1" :disabled="loading || !selected" @click="submit">添加物品</BaseButton>
      </div>
    </div>
  </BaseModal>
</template>
