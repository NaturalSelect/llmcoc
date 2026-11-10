<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { CharacterCardFull, InventoryItem, RegenField } from '@/api/characterTypes'
import BaseModal from '@/components/base/BaseModal.vue'
import { useAuthStore } from '@/stores/auth'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'
import { characterApi } from './characterApi'
import CharacterSection from './CharacterSection.vue'
import CharacterVitals from './CharacterVitals.vue'
import ChipList from './ChipList.vue'
import EntryList from './EntryList.vue'
import ItemDetailTip from './ItemDetailTip.vue'
import RegenBlock from './RegenBlock.vue'
import { useConfirmGuard } from './useConfirmGuard'
import {
  REGEN_SPECS,
  detailSubtitle,
  errMessage,
  mergeUpdatedCard,
  regenConfirmMessage,
  regenCost,
  sortedSkills,
  splitInventory,
  type Entry,
  type RegenSpec,
} from './characterLogic'

// NOTE: 旧前端的 charDetail 弹窗同时被首页、房间调查员列表、游戏页菜单打开；这里只依赖 card 属性，
// 其他页面传入人物卡即可复用（操作结果会同步到全局 characters store）
const props = defineProps<{ open: boolean; card: CharacterCardFull | null }>()
const emit = defineEmits<{ close: [] }>()

const auth = useAuthStore()
const chars = useCharactersStore()
const ui = useUiStore()
const guard = useConfirmGuard()
const onClose = guard.guardClose(() => emit('close'))

const current = ref<CharacterCardFull | null>(null)
const split = computed(() => splitInventory(current.value?.inventory ?? []))
const busy = ref(false)
const guidance = reactive<Record<RegenField, string>>({ appearance: '', backstory: '', traits: '' })
const regenBusy = reactive<Record<RegenField, boolean>>({ appearance: false, backstory: false, traits: false })

let freshSeq = 0

// NOTE: 传入的卡可能是列表或房间快照里的旧数据，所以打开时先显示它，再向服务端拉最新；
// 请求期间如果换了卡或已有别的操作更新了卡，这次结果就丢弃。单卡接口不计算 in_session，沿用传入值
async function refreshCard(card: CharacterCardFull | null): Promise<void> {
  const id = card?.id
  if (!id) return
  const seq = ++freshSeq
  try {
    const fresh = await characterApi.get(id)
    if (seq !== freshSeq || current.value?.id !== id) return
    const merged = mergeUpdatedCard(current.value, fresh)
    chars.syncCharacter(merged)
    current.value = merged
  } catch (e) {
    if (seq === freshSeq) ui.toast(errMessage(e), 'error')
  }
}

watch(
  () => [props.open, props.card] as const,
  ([open, card]) => {
    if (!open) return
    current.value = card
    guidance.appearance = guidance.backstory = guidance.traits = ''
    void refreshCard(card)
  },
  { immediate: true },
)

// NOTE: 请求期间弹窗可能已关闭或换了另一张卡：结果总是同步进 store，只有仍在展示同一张卡时才更新弹窗
function latestCard(id: number | undefined, fallback: CharacterCardFull): CharacterCardFull {
  if (current.value && current.value.id === id) return current.value
  return (chars.characters.find((c) => c.id === id) as CharacterCardFull | undefined) ?? fallback
}

function applyUpdate(updated: CharacterCardFull): void {
  freshSeq++
  const prev = latestCard(updated.id, updated)
  const merged = mergeUpdatedCard(prev, updated)
  chars.syncCharacter(merged)
  if (current.value && current.value.id === updated.id) current.value = merged
}

async function mutate(action: () => Promise<CharacterCardFull>, done: string): Promise<void> {
  busy.value = true
  try {
    applyUpdate(await action())
    ui.toast(done)
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    busy.value = false
  }
}

async function regen(spec: RegenSpec): Promise<void> {
  const card = current.value
  if (!card?.id) return
  const cost = regenCost(auth.shopCosts, spec)
  if (!(await guard.confirm(regenConfirmMessage(spec, cost, card.name), { confirmText: '重新生成' }))) return
  regenBusy[spec.field] = true
  try {
    const r = await characterApi.regenerate(card.id, spec.field, guidance[spec.field].trim())
    if (auth.user && typeof r.coins === 'number') auth.user.coins = r.coins
    applyUpdate({ ...latestCard(card.id, card), [spec.field]: r[spec.field] })
    ui.toast(spec.doneToast)
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    regenBusy[spec.field] = false
  }
}

function deposit(it: InventoryItem): Promise<void> | void {
  const id = current.value?.id
  if (!id) return
  return mutate(async () => (await characterApi.deposit(id, it)).character_card, '已存入仓库')
}

function equip(it: InventoryItem): Promise<void> | void {
  const id = current.value?.id
  if (!id) return
  return mutate(() => characterApi.setEquipped(id, it, true), '已装备')
}

function unequip(it: InventoryItem): Promise<void> | void {
  const id = current.value?.id
  if (!id) return
  return mutate(() => characterApi.setEquipped(id, it, false), '已卸下')
}

function discard(it: InventoryItem): Promise<void> | void {
  const id = current.value?.id
  if (!id) return
  return mutate(() => characterApi.removeInventoryItem(id, it), '已从物品栏移除')
}

async function forgetSpell(spell: string): Promise<void> {
  const id = current.value?.id
  if (!id || !spell) return
  if (!(await guard.confirm(`确定要遗忘法术「${spell}」吗？遗忘后克苏鲁神话技能-1，理智上限将相应提升，此操作不可撤销。`, { danger: true, confirmText: '遗忘' }))) return
  await mutate(() => characterApi.forgetSpell(id, spell), '已遗忘法术')
}

async function removeRelation(name: string | undefined): Promise<void> {
  const id = current.value?.id
  if (!id || !name) return
  if (!(await guard.confirm(`确定要删除与「${name}」的社会关系吗？此操作不可撤销。`, { danger: true, confirmText: '删除' }))) return
  await mutate(() => characterApi.removeSocialRelation(id, name), '已删除社会关系')
}

async function removeAsset(name: string | undefined): Promise<void> {
  const id = current.value?.id
  if (!id || !name) return
  if (!(await guard.confirm(`确定要删除资产「${name}」吗？此操作不可撤销。`, { danger: true, confirmText: '删除' }))) return
  await mutate(() => characterApi.removeAsset(id, name), '已删除资产')
}

function relationEntries(card: CharacterCardFull): Entry[] {
  return (card.social_relations ?? []).map((r) => ({
    title: r?.name || '未命名关系',
    lines: [`关系：${r?.relationship || '—'}`, ...(r?.note ? [r.note] : [])],
    key: r?.name,
  }))
}

function assetEntries(card: CharacterCardFull): Entry[] {
  return (card.assets ?? []).map((a) => ({
    title: a?.name || '未命名资产',
    lines: [`类别：${a?.category || '—'}`, ...(a?.note ? [a.note] : [])],
    key: a?.name,
  }))
}
</script>

<template>
  <BaseModal :open="open" :title="current?.name ?? ''" max-width="max-w-2xl" @close="onClose">
    <template v-if="current">
      <p class="mb-5 text-sm text-fg-muted">{{ detailSubtitle(current) }}</p>

      <CharacterVitals :card="current" class="mb-5" />

      <CharacterSection title="技能（完整）">
        <div class="mb-2 text-xs tabular-nums text-fg-subtle">总计 {{ Object.keys(current.skills ?? {}).length }} 项</div>
        <ul class="grid grid-cols-2 gap-2 sm:grid-cols-3">
          <li
            v-for="[skill, val] in sortedSkills(current.skills)"
            :key="skill"
            class="flex items-center justify-between gap-2 rounded-lg bg-surface-2 px-3 py-2 text-xs"
          >
            <span class="truncate text-fg-muted">{{ skill }}</span>
            <span class="shrink-0 font-semibold tabular-nums text-fg">{{ val }}</span>
          </li>
        </ul>
      </CharacterSection>

      <CharacterSection title="装备栏">
        <ChipList :items="split.equipped.map((it) => it.name)">
          <template #label="{ index }"><ItemDetailTip :item="split.equipped[index]" /></template>
          <template #actions="{ index }">
            <button
              type="button"
              class="rounded-full px-2 py-0.5 text-xs text-info transition hover:bg-info/15 disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="busy"
              @click="unequip(split.equipped[index])"
            >
              卸下
            </button>
          </template>
        </ChipList>
      </CharacterSection>

      <CharacterSection title="物品栏">
        <p class="mb-2 text-xs text-fg-subtle">鼠标放到物品上或点击可查看详情；点击物品右侧按钮可直接装备、丢弃或存入账号仓库。</p>
        <p v-if="current.in_session" class="mb-2 text-xs text-warning" role="status">该人物卡正在游戏中，无法存取仓库。</p>
        <ChipList :items="split.carried.map((it) => it.name)">
          <template #label="{ index }"><ItemDetailTip :item="split.carried[index]" /></template>
          <template #actions="{ index }">
            <button
              type="button"
              class="rounded-full px-2 py-0.5 text-xs text-info transition hover:bg-info/15 disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="busy"
              @click="equip(split.carried[index])"
            >
              装备
            </button>
            <button
              type="button"
              class="rounded-full px-2 py-0.5 text-xs text-info transition hover:bg-info/15 disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="current.in_session || busy"
              @click="deposit(split.carried[index])"
            >
              存入仓库
            </button>
            <button
              type="button"
              class="rounded-full px-2 py-0.5 text-xs text-danger transition hover:bg-danger/15 disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="busy"
              @click="discard(split.carried[index])"
            >
              丢弃
            </button>
          </template>
        </ChipList>
      </CharacterSection>

      <CharacterSection title="法术">
        <ChipList :items="current.spells ?? []" tone="ledger">
          <template #actions="{ item }">
            <button
              type="button"
              class="rounded-full px-2 py-0.5 text-xs text-danger transition hover:bg-danger/15 disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="busy"
              @click="forgetSpell(item)"
            >
              遗忘
            </button>
          </template>
        </ChipList>
      </CharacterSection>

      <CharacterSection title="已知神话存在">
        <ChipList :items="current.seen_monsters ?? []" tone="seal" />
      </CharacterSection>

      <CharacterSection title="社会关系">
        <EntryList :entries="relationEntries(current)" :busy="busy" @remove="removeRelation" />
      </CharacterSection>

      <CharacterSection title="资产">
        <EntryList :entries="assetEntries(current)" :busy="busy" @remove="removeAsset" />
      </CharacterSection>

      <template v-for="spec in REGEN_SPECS" :key="spec.field">
        <RegenBlock
          v-if="current[spec.field]"
          v-model:guidance="guidance[spec.field]"
          :spec="spec"
          :text="current[spec.field] ?? ''"
          :cost="regenCost(auth.shopCosts, spec)"
          :coins="auth.user?.coins ?? 0"
          :busy="regenBusy[spec.field]"
          @regen="regen(spec)"
        />
      </template>

      <CharacterSection title="创建/更新时间">
        <p class="font-mono text-xs text-fg-muted">created_at: {{ current.created_at || '' }}</p>
        <p class="font-mono text-xs text-fg-muted">updated_at: {{ current.updated_at || '' }}</p>
      </CharacterSection>
    </template>
  </BaseModal>
</template>
