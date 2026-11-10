<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { CharacterCardFull, InventoryItem } from '@/api/characterTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import FormField from '@/components/base/FormField.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'
import { characterApi } from './characterApi'
import { errMessage, itemLabel, warehouseOptionLabel, warehouseUsableCharacters } from './characterLogic'
import ChipList from './ChipList.vue'
import ItemDetailTip from './ItemDetailTip.vue'
import { useConfirmGuard } from './useConfirmGuard'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const chars = useCharactersStore()
const ui = useUiStore()
const guard = useConfirmGuard()
const onClose = guard.guardClose(() => emit('close'))

const items = ref<InventoryItem[]>([])
const loading = ref(false)
const busy = ref(false)
const targetId = ref<number | ''>('')

const usable = computed(() => warehouseUsableCharacters(chars.characters as CharacterCardFull[]))

async function load(): Promise<void> {
  loading.value = true
  try {
    items.value = (await characterApi.warehouse()).warehouse || []
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    loading.value = false
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) return
    targetId.value = usable.value[0]?.id ?? ''
    void load()
  },
  { immediate: true },
)

async function withdraw(it: InventoryItem): Promise<void> {
  if (!targetId.value) {
    ui.toast('请先选择接收物品的人物卡', 'error')
    return
  }
  busy.value = true
  try {
    const r = await characterApi.withdraw(targetId.value, it)
    chars.syncCharacter(r.character_card)
    items.value = r.warehouse || []
    ui.toast('已取出到人物卡')
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    busy.value = false
  }
}

async function discard(it: InventoryItem): Promise<void> {
  if (!(await guard.confirm(`确定要丢弃仓库中的「${itemLabel(it)}」吗？此操作不可撤销。`, { danger: true, confirmText: '丢弃' }))) return
  busy.value = true
  try {
    items.value = (await characterApi.discard(it)).warehouse || []
    ui.toast('已丢弃')
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <BaseModal :open="open" title="账号仓库" max-width="max-w-md" @close="onClose">
    <div class="space-y-4">
      <p class="text-xs text-fg-subtle">物品可在同账号的不同人物卡之间流通，正在游戏中的人物卡无法存取。</p>

      <FormField label="取出到人物卡">
        <BaseSelect v-model="targetId">
          <option value="">— 请选择 —</option>
          <option v-for="c in usable" :key="c.id" :value="c.id">{{ warehouseOptionLabel(c) }}</option>
        </BaseSelect>
      </FormField>
      <p v-if="!usable.length" class="-mt-2 text-xs text-warning">所有人物卡都在游戏中，暂时无法取出物品。</p>

      <section>
        <SectionHeading class="mb-2" :count="loading ? undefined : items.length">仓库物品</SectionHeading>
        <div v-if="loading" class="text-xs text-fg-subtle" role="status">加载中…</div>
        <ChipList v-else-if="items.length" :items="items.map((it) => it.name)">
          <template #label="{ index }"><ItemDetailTip :item="items[index]" /></template>
          <template #actions="{ index }">
            <button
              type="button"
              class="rounded-full px-2 py-0.5 text-xs text-info transition hover:bg-info/15 disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="!targetId || busy"
              @click="withdraw(items[index])"
            >
              取出
            </button>
            <button
              type="button"
              class="rounded-full px-2 py-0.5 text-xs text-danger transition hover:bg-danger/15 disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="busy"
              @click="discard(items[index])"
            >
              丢弃
            </button>
          </template>
        </ChipList>
        <p v-else class="text-xs text-fg-subtle">仓库空空如也。</p>
      </section>

      <div class="flex gap-2 pt-1">
        <BaseButton variant="ghost" class="flex-1" @click="emit('close')">关闭</BaseButton>
      </div>
    </div>
  </BaseModal>
</template>
