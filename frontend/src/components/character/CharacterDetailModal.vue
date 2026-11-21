<script setup lang="ts">
import { BookOpen, HeartPulse } from '@lucide/vue'
import { computed, reactive, ref, watch } from 'vue'
import type { CharacterCardFull, InventoryItem, RegenField } from '@/api/characterTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import ImagePreview from '@/components/game/ImagePreview.vue'
import { useAuthStore } from '@/stores/auth'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'
import { characterApi } from './characterApi'
import CharacterPortrait from './CharacterPortrait.vue'
import CharacterSection from './CharacterSection.vue'
import CharacterVitals from './CharacterVitals.vue'
import ChipList from './ChipList.vue'
import EntryList from './EntryList.vue'
import ItemDetailTip, { clearPinnedItemDetail } from './ItemDetailTip.vue'
import RegenBlock from './RegenBlock.vue'
import { useConfirmGuard } from './useConfirmGuard'
import {
  REGEN_SPECS,
  detailSubtitle,
  errMessage,
  mergeUpdatedCard,
  portraitConfirmMessage,
  portraitCost,
  portraitSrc,
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
const emit = defineEmits<{ close: []; portrait: [] }>()

const auth = useAuthStore()
const chars = useCharactersStore()
const ui = useUiStore()
const guard = useConfirmGuard()

const current = ref<CharacterCardFull | null>(null)
const split = computed(() => splitInventory(current.value?.inventory ?? []))
const busy = ref(false)
const downtimeBusy = ref(false)
const guidance = reactive<Record<RegenField, string>>({ appearance: '', backstory: '', traits: '' })
const regenBusy = reactive<Record<RegenField, boolean>>({ appearance: false, backstory: false, traits: false })
// NOTE: 生成可能持续数分钟，期间弹窗可能被关掉或换成另一张卡，所以按卡 id 记录生成中状态，重新打开同一张卡仍显示生成中
const portraitBusyIds = reactive(new Set<number>())
const portraitBusy = computed(() => current.value?.id !== undefined && portraitBusyIds.has(current.value.id))

// NOTE: 幕间休整仅对本人存活卡开放；测试固件未填 user_id 时按本人处理，后端会做最终归属校验
const canDowntime = computed(() => {
  const c = current.value
  if (!c?.id) return false
  if (c.is_active === false || c.wound_state === 'dead') return false
  return !c.user_id || c.user_id === auth.user?.id
})
const sanatoriumCost = computed(() => Number(auth.shopCosts.sanatorium_cost ?? 50))
const needsSanatorium = computed(() => {
  const c = current.value
  if (!c) return false
  const madness = c.madness_state ?? 'none'
  if (madness === 'permanent') return false
  const san = Number(c.stats?.san ?? 0)
  const maxSan = Number(c.stats?.max_san ?? 0)
  return (maxSan > 0 && san < maxSan) || madness === 'temporary' || madness === 'indefinite'
})
const tomes = computed(() => (current.value?.inventory ?? []).filter((it) => it.type === 'tome'))
const sanTooLowForTome = computed(() => Number(current.value?.stats?.san ?? 0) <= 4)

const previewSrc = ref('')
let previewHold = 0

// NOTE: ImagePreview 和 BaseModal 都在 document 上监听 Esc，预览打开期间以及关闭后的同一次事件分发内（setTimeout 0 之后才解除），
// 都忽略弹窗的 close，否则一次 Esc 会把两层一起关掉
const onClose = guard.guardClose(() => {
  if (previewSrc.value || previewHold > 0) return
  emit('close')
})

function openPreview(): void {
  previewSrc.value = portraitSrc(current.value)
}

// NOTE: ImagePreview 每次 Esc 都会触发 close；没有预览时必须直接返回，否则会白白设置忽略窗口，把弹窗自己的 Esc 关闭吞掉
function closePreview(): void {
  if (!previewSrc.value) return
  previewSrc.value = ''
  previewHold++
  setTimeout(() => {
    previewHold--
  }, 0)
}

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
    previewSrc.value = ''
    if (!open || card?.id !== current.value?.id) clearPinnedItemDetail()
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

async function generatePortrait(): Promise<void> {
  const card = current.value
  const id = card?.id
  if (!card || !id || portraitBusyIds.has(id)) return
  const regenerate = !!portraitSrc(card)
  const cost = portraitCost(auth.shopCosts)
  if (!(await guard.confirm(portraitConfirmMessage(cost, card.name, regenerate), { confirmText: regenerate ? '重新生成' : '生成' }))) return
  portraitBusyIds.add(id)
  try {
    const r = await characterApi.generatePortrait(id)
    if (auth.user && typeof r.coins === 'number') auth.user.coins = r.coins
    applyUpdate({ ...latestCard(id, card), portrait_hash: r.portrait_hash })
    ui.toast(regenerate ? '立绘已重新生成' : '立绘已生成')
    emit('portrait')
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    portraitBusyIds.delete(id)
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

async function visitSanatorium(): Promise<void> {
  const card = current.value
  const id = card?.id
  if (!card || !id || downtimeBusy.value) return
  const cost = sanatoriumCost.value
  const msg = `确认送「${card.name || '该调查员'}」入住阿卡姆疗养院？将消耗 ${cost} 金币，恢复 1D6+2 点理智并平复临时性/不定性疯狂状态。`
  if (!(await guard.confirm(msg, { confirmText: '入住疗养院' }))) return
  downtimeBusy.value = true
  try {
    const r = await characterApi.sanatorium(id)
    if (auth.user && typeof r.coins_left === 'number') auth.user.coins = r.coins_left
    applyUpdate(r.card)
    ui.toast(r.message || '疗养完成')
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    downtimeBusy.value = false
  }
}

async function studyTome(tomeName: string): Promise<void> {
  const card = current.value
  const id = card?.id
  if (!card || !id || !tomeName || downtimeBusy.value) return
  const msg = `确认研读神话典籍《${tomeName}》？将进行智力检定并损失 1D4 点理智（至少保留 1 点），换取克苏鲁神话技能提升（同时降低理智上限）。`
  if (!(await guard.confirm(msg, { confirmText: '开始研读' }))) return
  downtimeBusy.value = true
  try {
    const r = await characterApi.studyTome(id, tomeName)
    applyUpdate(r.card)
    ui.toast(r.message || `研读《${tomeName}》完成`)
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    downtimeBusy.value = false
  }
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

      <CharacterPortrait
        :src="portraitSrc(current)"
        :cost="portraitCost(auth.shopCosts)"
        :coins="auth.user?.coins ?? 0"
        :busy="portraitBusy"
        @generate="generatePortrait"
        @preview="openPreview"
      />

      <CharacterVitals :card="current" class="mb-5" />

      <CharacterSection v-if="canDowntime" title="幕间休整">
        <p v-if="current.in_session" class="mb-2 text-xs text-warning" role="status">
          该人物卡正在游戏中，需结团后才能进行幕间休整。
        </p>
        <div class="space-y-2.5">
          <div class="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-line bg-surface-2 px-3 py-2.5">
            <div class="min-w-0">
              <div class="flex items-center gap-1.5 text-sm font-medium text-fg">
                <HeartPulse :size="15" :stroke-width="1.75" class="shrink-0 text-san" />
                <span>阿卡姆疗养院</span>
              </div>
              <p class="mt-0.5 text-xs text-fg-subtle">
                <template v-if="current.madness_state === 'permanent'">永久性疯狂无法通过疗养院治愈。</template>
                <template v-else-if="needsSanatorium">花费 {{ sanatoriumCost }} 金币，恢复 1D6+2 理智并清除临时/不定性疯狂。</template>
                <template v-else>当前理智充沛且无疯狂症状，无需疗养。</template>
              </p>
            </div>
            <BaseButton
              size="sm"
              variant="secondary"
              :loading="downtimeBusy"
              :disabled="current.in_session || !needsSanatorium || (auth.user?.coins ?? 0) < sanatoriumCost"
              @click="visitSanatorium"
            >
              入住疗养院（{{ sanatoriumCost }} 金币）
            </BaseButton>
          </div>

          <div
            v-for="tome in tomes"
            :key="tome.name"
            class="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-line bg-surface-2 px-3 py-2.5"
          >
            <div class="min-w-0">
              <div class="flex items-center gap-1.5 text-sm font-medium text-fg">
                <BookOpen :size="15" :stroke-width="1.75" class="shrink-0 text-accent" />
                <span class="truncate">研读《{{ tome.name }}》</span>
              </div>
              <p class="mt-0.5 text-xs text-fg-subtle">
                <template v-if="sanTooLowForTome">当前理智过低（≤ 4），贸然研读神话典籍风险过高。</template>
                <template v-else>智力检定：损失 1D4 理智，成功 +1D3 克苏鲁神话，失败 +1。</template>
              </p>
            </div>
            <BaseButton
              size="sm"
              variant="secondary"
              :loading="downtimeBusy"
              :disabled="current.in_session || sanTooLowForTome"
              @click="studyTome(tome.name)"
            >
              开始研读
            </BaseButton>
          </div>
        </div>
      </CharacterSection>

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
        <p class="mb-2 text-xs text-fg-subtle">鼠标放到物品上可查看详情，点击可固定详情（再次点击取消）；点击物品右侧按钮可直接装备、丢弃或存入账号仓库。</p>
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
              :disabled="current.in_session || busy || split.carried[index].type === 'story'"
              :title="split.carried[index].type === 'story' ? '剧情物品无法存入仓库' : undefined"
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
  <ImagePreview :src="previewSrc" @close="closePreview" />
</template>
