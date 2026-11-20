<script setup lang="ts">
import { RefreshCw, Undo2, UserRound } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { companionApi } from '@/api/companionApi'
import type { AdminCompanionCard } from '@/api/companionTypes'
import { errMsg } from '@/components/admin/shared/errMsg'
import { useToastRun } from '@/components/admin/shared/useToastRun'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import Skeleton from '@/components/base/Skeleton.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import StatBar from '@/components/base/StatBar.vue'
import { portraitSrc } from '@/components/character/characterLogic'
import { cardOptionLabel } from '@/components/session/sessionLogic'
import { COMPANION_POOL_LABEL, companionCardMeta, companionPoolState, poolCandidates } from '@/game/companion'
import type { CompanionPoolState } from '@/game/companion'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'

// NOTE: 管理员的 AI 卡池：卡池里的卡属于 bot 用户，房主加 AI 时从这里挑空闲的卡。
// 「从我的卡移入」的候选取自 /api/characters（只含自己名下的活跃卡，复用人物卡 store），已在房间里的卡不能移入；
// 对局中的卡不能移回（后端同样拒绝），要等房间结束或房主移除 AI
const ui = useUiStore()
const run = useToastRun()
const chars = useCharactersStore()

const cards = ref<AdminCompanionCard[]>([])
const loading = ref(false)
const loaded = ref(false)
const pickId = ref<number | ''>('')
const adding = ref(false)
const returningId = ref<number | null>(null)

const candidates = computed(() => poolCandidates(chars.characters))

const stateTone: Record<CompanionPoolState, 'moss' | 'ledger' | 'danger'> = {
  idle: 'moss',
  in_session: 'ledger',
  inactive: 'danger',
}

async function loadPool() {
  cards.value = await companionApi.adminList()
  loaded.value = true
}

// NOTE: 卡池和自己的卡分开请求、分开报错：任何一边失败都不应让另一边停在旧数据上
async function reload() {
  loading.value = true
  try {
    await Promise.all([run(loadPool), run(() => chars.loadCharacters())])
  } finally {
    loading.value = false
  }
  // NOTE: 候选列表刷新后，选中的卡可能已经被移走或被房间占用，此时清掉选择
  if (pickId.value !== '' && !candidates.value.some((c) => c.id === pickId.value)) pickId.value = ''
}

onMounted(reload)

async function addToPool() {
  const id = pickId.value
  if (id === '' || adding.value) return
  const name = candidates.value.find((c) => c.id === id)?.name || `#${id}`
  if (!(await ui.confirm(`把「${name}」移入 AI 卡池？移入后它归 AI 陪玩使用，不再出现在你的人物卡里。`, { confirmText: '移入' }))) return
  adding.value = true
  try {
    const res = await companionApi.adminAdd(id)
    ui.toast(res?.message || `已把「${name}」移入 AI 卡池`)
    pickId.value = ''
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    adding.value = false
  }
  await reload()
}

async function giveBack(c: AdminCompanionCard) {
  if (c.in_session || returningId.value !== null) return
  if (!(await ui.confirm(`把「${c.name}」移回你名下？移回后 AI 陪玩不能再使用它。`, { confirmText: '移回' }))) return
  returningId.value = c.id
  try {
    const res = await companionApi.adminReturn(c.id)
    ui.toast(res?.message || `已把「${c.name}」移回你名下`)
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    returningId.value = null
  }
  await reload()
}
</script>

<template>
  <div class="space-y-5">
    <PaperCard class="space-y-3">
      <div class="min-w-0">
        <SectionHeading>移入卡池</SectionHeading>
        <p class="mt-1 text-xs text-fg-subtle">选一张你名下、没有在房间里的人物卡。移入后它属于 AI 陪玩，房主可以在大厅里把它加进房间。</p>
      </div>
      <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
        <BaseSelect v-model="pickId" class="sm:max-w-sm" aria-label="选择要移入卡池的人物卡" :disabled="candidates.length === 0">
          <option value="">{{ candidates.length === 0 ? '没有可移入的人物卡' : '选择人物卡' }}</option>
          <option v-for="c in candidates" :key="c.id" :value="c.id">{{ cardOptionLabel(c) }}</option>
        </BaseSelect>
        <BaseButton class="shrink-0" :disabled="pickId === ''" :loading="adding" @click="addToPool">从我的卡移入</BaseButton>
      </div>
    </PaperCard>

    <PaperCard>
      <div class="mb-4 flex items-center justify-between gap-3">
        <SectionHeading :count="cards.length">AI 卡池</SectionHeading>
        <BaseButton variant="ghost" size="sm" :loading="loading" @click="reload">
          <RefreshCw :size="16" :stroke-width="1.75" />
          刷新
        </BaseButton>
      </div>

      <div v-if="!loaded && loading" class="space-y-2">
        <Skeleton class="h-20" />
        <Skeleton class="h-20" />
      </div>
      <EmptyState v-else-if="cards.length === 0">卡池里还没有人物卡，从上面的人物卡里移入一张吧</EmptyState>
      <ul v-else class="space-y-2">
        <li v-for="c in cards" :key="c.id" class="flex flex-col gap-3 rounded-lg bg-surface-2 p-3 sm:flex-row sm:items-center">
          <div class="flex min-w-0 flex-1 items-center gap-3">
            <img v-if="portraitSrc(c)" :src="portraitSrc(c)" alt="" class="h-16 w-11 shrink-0 rounded-lg bg-surface-3 object-cover object-top" />
            <span v-else class="flex h-16 w-11 shrink-0 items-center justify-center rounded-lg bg-surface-3 text-fg-subtle" aria-hidden="true">
              <UserRound :size="20" :stroke-width="1.5" />
            </span>
            <div class="min-w-0 flex-1 space-y-1.5">
              <div class="flex flex-wrap items-center gap-2">
                <span class="truncate text-sm font-medium text-fg">{{ c.name }}</span>
                <StampBadge :tone="stateTone[companionPoolState(c)]">{{ COMPANION_POOL_LABEL[companionPoolState(c)] }}</StampBadge>
              </div>
              <p class="truncate text-xs text-fg-subtle">{{ companionCardMeta(c) }}</p>
              <div class="grid max-w-xs gap-1">
                <StatBar label="HP" tone="hp" :value="c.hp" :max="c.max_hp" />
                <StatBar label="SAN" tone="san" :value="c.san" :max="c.max_san" />
              </div>
            </div>
          </div>
          <BaseButton
            class="shrink-0 self-start sm:self-center"
            variant="ghost"
            size="sm"
            :disabled="c.in_session"
            :loading="returningId === c.id"
            :title="c.in_session ? '正在房间里使用，房间结束或房主移除 AI 后才能移回' : undefined"
            @click="giveBack(c)"
          >
            <Undo2 :size="16" :stroke-width="1.75" />
            移回我名下
          </BaseButton>
        </li>
      </ul>
    </PaperCard>
  </div>
</template>
