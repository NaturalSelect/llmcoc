<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { CharacterCardFull } from '@/api/characterTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import FormField from '@/components/base/FormField.vue'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'
import { characterApi } from './characterApi'
import { errMessage, warehouseOptionLabel, warehouseUsableCharacters } from './characterLogic'
import ChipList from './ChipList.vue'
import { useConfirmGuard } from './useConfirmGuard'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const chars = useCharactersStore()
const ui = useUiStore()
const guard = useConfirmGuard()
const onClose = guard.guardClose(() => emit('close'))

const items = ref<string[]>([])
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

async function withdraw(item: string): Promise<void> {
  if (!item) return
  if (!targetId.value) {
    ui.toast('请先选择接收物品的人物卡', 'error')
    return
  }
  busy.value = true
  try {
    const r = await characterApi.withdraw(targetId.value, item)
    chars.syncCharacter(r.character_card)
    items.value = r.warehouse || []
    ui.toast('已取出到人物卡')
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    busy.value = false
  }
}

async function discard(item: string): Promise<void> {
  if (!item) return
  if (!(await guard.confirm(`确定要丢弃仓库中的「${item}」吗？此操作不可撤销。`, { danger: true, confirmText: '丢弃' }))) return
  busy.value = true
  try {
    items.value = (await characterApi.discard(item)).warehouse || []
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
    <div class="space-y-3">
      <p class="text-xs text-ink-faint">物品可在同账号的不同人物卡之间流通，正在游戏中的人物卡无法存取。</p>

      <FormField label="取出到人物卡">
        <BaseSelect v-model="targetId">
          <option value="">— 请选择 —</option>
          <option v-for="c in usable" :key="c.id" :value="c.id">{{ warehouseOptionLabel(c) }}</option>
        </BaseSelect>
      </FormField>
      <p v-if="!usable.length" class="-mt-2 text-[11px] text-ink-faint">所有人物卡都在游戏中，暂时无法取出物品。</p>

      <div>
        <div class="mb-1 font-type text-xs text-ink-soft">仓库物品</div>
        <div v-if="loading" class="text-xs text-ink-faint" role="status">加载中…</div>
        <ChipList v-else-if="items.length" :items="items">
          <template #actions="{ item }">
            <button
              type="button"
              class="leading-none text-ledger transition hover:underline disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="!targetId || busy"
              @click="withdraw(item)"
            >
              取出
            </button>
            <button type="button" class="leading-none text-seal transition hover:underline disabled:opacity-40" :disabled="busy" @click="discard(item)">丢弃</button>
          </template>
        </ChipList>
        <p v-else class="text-xs text-ink-faint">仓库空空如也。</p>
      </div>

      <div class="flex gap-2 pt-1">
        <BaseButton variant="ghost" class="flex-1" @click="emit('close')">关闭</BaseButton>
      </div>
    </div>
  </BaseModal>
</template>
