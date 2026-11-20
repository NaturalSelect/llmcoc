<script setup lang="ts">
import { Award, Brain, Dices, Eye, ShieldAlert, Timer } from '@lucide/vue'
import { ref, watch } from 'vue'
import { api } from '@/api/client'
import type { ScenarioClue, ScenarioEnding } from '@/api/sessionTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import StampBadge from '@/components/base/StampBadge.vue'
import { attitudeTone, clueNatureLabel, clueNatureTone } from '@/components/scenario/scenarioFormat'
import type { SessionClock } from '@/game/handbookTypes'
import { useUiStore } from '@/stores/ui'

interface DebriefEvalPlayer {
  character_name: string
  comment: string
  score: number
  base_coins: number
  bonus_coins: number
}

interface DebriefDiceStat {
  character: string
  total: number
  criticals: number
  fumbles: number
  successes: number
  failures: number
  best_roll: number
  worst_roll: number
}

interface DebriefNPC {
  name: string
  occupation?: string
  attitude?: string
  description?: string
}

interface DebriefHallucination {
  recipient_name: string
  content: string
  created_at: string
}

interface DebriefResponse {
  session_name: string
  scenario_name: string
  ended_at: string
  evaluation: { players?: DebriefEvalPlayer[] } | null
  dice_stats: DebriefDiceStat[]
  clues_found: number
  total_clues: number
  truth: {
    core_truth?: string
    antagonist_dossier?: string
    mythos_core?: string
    endings: ScenarioEnding[]
    clues: ScenarioClue[]
    npcs: DebriefNPC[]
    clocks?: SessionClock[]
    hallucinations?: DebriefHallucination[]
  }
}

const props = defineProps<{ open: boolean; sessionId: number | null }>()
const emit = defineEmits<{ close: [] }>()

const ui = useUiStore()
const tab = ref<'stats' | 'truth'>('stats')
const loading = ref(false)
const truthRevealed = ref(false)
const data = ref<DebriefResponse | null>(null)

async function loadDebrief() {
  if (!props.sessionId) return
  loading.value = true
  try {
    data.value = await api<DebriefResponse>('GET', `/api/sessions/${props.sessionId}/debrief`)
  } catch (e) {
    ui.toast(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    loading.value = false
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) {
      tab.value = 'stats'
      truthRevealed.value = false
      return
    }
    void loadDebrief()
  },
)

function successRate(st: DebriefDiceStat): string {
  if (!st.total) return '0%'
  return Math.round((st.successes / st.total) * 100) + '%'
}
</script>

<template>
  <BaseModal :open="open" title="结团复盘与真相" max-width="max-w-2xl" @close="emit('close')">
    <div class="mb-4 flex gap-1 border-b border-line pb-2" role="tablist" aria-label="复盘分页">
      <button
        type="button"
        role="tab"
        :aria-selected="tab === 'stats'"
        class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium transition"
        :class="tab === 'stats' ? 'bg-accent/15 text-accent' : 'text-fg-subtle hover:bg-surface-2 hover:text-fg'"
        @click="tab = 'stats'"
      >
        <Award :size="15" :stroke-width="1.75" />
        战绩与骰运
      </button>
      <button
        type="button"
        role="tab"
        :aria-selected="tab === 'truth'"
        class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium transition"
        :class="tab === 'truth' ? 'bg-accent/15 text-accent' : 'text-fg-subtle hover:bg-surface-2 hover:text-fg'"
        @click="tab = 'truth'"
      >
        <Eye :size="15" :stroke-width="1.75" />
        掀开底牌（剧透）
      </button>
    </div>

    <p v-if="loading" class="py-8 text-center text-sm text-fg-subtle">加载结团档案中…</p>

    <template v-else-if="data">
      <!-- Tab 1: 战绩与骰运 -->
      <div v-if="tab === 'stats'" class="space-y-5">
        <div class="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-line bg-surface-2/70 px-3.5 py-2.5 text-sm">
          <div>
            <span class="text-fg-subtle">模组：</span>
            <span class="font-medium text-fg">{{ data.scenario_name || data.session_name }}</span>
          </div>
          <div class="tabular-nums text-fg-muted">
            线索收集：<span class="font-medium text-accent">{{ data.clues_found }}</span> / {{ data.total_clues }}
          </div>
        </div>

        <section class="space-y-2.5">
          <h3 class="text-sm font-semibold text-fg">KP 结算评语</h3>
          <div v-if="data.evaluation?.players?.length" class="space-y-2">
            <article
              v-for="p in data.evaluation.players"
              :key="p.character_name"
              class="rounded-lg border border-line bg-surface-2/60 p-3"
            >
              <div class="flex flex-wrap items-center justify-between gap-2">
                <span class="font-medium text-fg">{{ p.character_name }}</span>
                <div class="flex items-center gap-2 text-xs tabular-nums">
                  <StampBadge tone="moss">评分 {{ p.score }}</StampBadge>
                  <span class="text-accent">+{{ p.base_coins + p.bonus_coins }} 金币</span>
                </div>
              </div>
              <p v-if="p.comment" class="mt-2 whitespace-pre-wrap text-sm leading-relaxed text-fg-muted">{{ p.comment }}</p>
            </article>
          </div>
          <p v-else class="rounded-lg bg-surface-2/50 px-3 py-2.5 text-sm text-fg-subtle">
            本局未产生 AI 结局评语（可能是手动提前结束）。
          </p>
        </section>

        <section class="space-y-2.5">
          <h3 class="flex items-center gap-1.5 text-sm font-semibold text-fg">
            <Dices :size="16" :stroke-width="1.75" class="text-accent" />
            公开骰运统计
          </h3>
          <div v-if="data.dice_stats.length" class="overflow-x-auto rounded-lg border border-line">
            <table class="w-full text-left text-xs">
              <thead class="bg-surface-2 text-fg-subtle">
                <tr>
                  <th class="px-3 py-2 font-medium">调查员</th>
                  <th class="px-3 py-2 font-medium">检定数</th>
                  <th class="px-3 py-2 font-medium">成功 / 失败</th>
                  <th class="px-3 py-2 font-medium">胜率</th>
                  <th class="px-3 py-2 font-medium">大成功 / 大失败</th>
                  <th class="px-3 py-2 font-medium">最佳 / 最差</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-line tabular-nums">
                <tr v-for="st in data.dice_stats" :key="st.character" class="text-fg-muted">
                  <td class="px-3 py-2 font-medium text-fg">{{ st.character }}</td>
                  <td class="px-3 py-2">{{ st.total }}</td>
                  <td class="px-3 py-2">
                    <span class="text-success">{{ st.successes }}</span> / <span class="text-danger">{{ st.failures }}</span>
                  </td>
                  <td class="px-3 py-2">{{ successRate(st) }}</td>
                  <td class="px-3 py-2">
                    <span class="text-accent">{{ st.criticals }}</span> / <span class="text-danger">{{ st.fumbles }}</span>
                  </td>
                  <td class="px-3 py-2">{{ st.best_roll || '-' }} / {{ st.worst_roll || '-' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-else class="rounded-lg bg-surface-2/50 px-3 py-2.5 text-sm text-fg-subtle">本局暂无公开检定记录。</p>
        </section>
      </div>

      <!-- Tab 2: 掀开底牌（剧透） -->
      <div v-else class="space-y-5">
        <div v-if="!truthRevealed" class="space-y-3 rounded-xl border border-line bg-surface-2/80 p-5 text-center">
          <ShieldAlert :size="28" :stroke-width="1.75" class="mx-auto text-warning" />
          <p class="text-sm font-medium text-fg">以下内容包含模组完整真相、全部结局与未发现线索</p>
          <p class="text-xs text-fg-subtle">如果你还打算以玩家身份重跑同一模组，请谨慎掀开底牌。</p>
          <div class="pt-1">
            <BaseButton @click="truthRevealed = true">确认揭晓模组真相与全部结局</BaseButton>
          </div>
        </div>

        <template v-else>
          <section v-if="data.truth.core_truth || data.truth.antagonist_dossier || data.truth.mythos_core" class="space-y-3">
            <h3 class="text-sm font-semibold text-fg">模组核心真相</h3>
            <div v-if="data.truth.core_truth" class="rounded-lg border border-line bg-surface-2/60 p-3">
              <p class="mb-1 text-xs font-medium text-accent">事件全貌</p>
              <p class="whitespace-pre-wrap text-sm leading-relaxed text-fg-muted">{{ data.truth.core_truth }}</p>
            </div>
            <div v-if="data.truth.mythos_core" class="rounded-lg border border-line bg-surface-2/60 p-3">
              <p class="mb-1 text-xs font-medium text-san">神话本质</p>
              <p class="whitespace-pre-wrap text-sm leading-relaxed text-fg-muted">{{ data.truth.mythos_core }}</p>
            </div>
            <div v-if="data.truth.antagonist_dossier" class="rounded-lg border border-line bg-surface-2/60 p-3">
              <p class="mb-1 text-xs font-medium text-danger">幕后施动者档案</p>
              <p class="whitespace-pre-wrap text-sm leading-relaxed text-fg-muted">{{ data.truth.antagonist_dossier }}</p>
            </div>
          </section>

          <section v-if="data.truth.clocks?.length" class="space-y-2.5">
            <h3 class="flex items-center gap-1.5 text-sm font-semibold text-fg">
              <Timer :size="15" :stroke-width="1.75" class="text-accent" />
              本局危机时钟（含 KP 暗钟）
            </h3>
            <div class="grid gap-2 sm:grid-cols-2">
              <article
                v-for="clk in data.truth.clocks"
                :key="clk.name"
                class="space-y-1.5 rounded-lg border border-line bg-surface-2/60 p-3 text-xs"
              >
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <span class="font-medium text-fg">{{ clk.name }}</span>
                  <div class="flex items-center gap-1.5">
                    <StampBadge v-if="!clk.visible" tone="faint">KP 暗钟</StampBadge>
                    <span class="font-semibold tabular-nums" :class="clk.current >= clk.max ? 'text-danger' : 'text-accent'">
                      {{ clk.current }} / {{ clk.max }}
                    </span>
                  </div>
                </div>
                <p v-if="clk.stage" class="text-fg-muted">最终阶段：{{ clk.stage }}</p>
                <p v-if="clk.description" class="text-fg-subtle">{{ clk.description }}</p>
              </article>
            </div>
          </section>

          <section v-if="data.truth.hallucinations?.length" class="space-y-2.5">
            <h3 class="flex items-center gap-1.5 text-sm font-semibold text-fg">
              <Brain :size="15" :stroke-width="1.75" class="text-san" />
              潜伏疯狂 · 幻觉耳语揭晓
            </h3>
            <div class="space-y-2">
              <article
                v-for="(h, idx) in data.truth.hallucinations"
                :key="idx"
                class="rounded-lg border border-san/35 bg-san/10 p-3"
              >
                <p class="text-xs font-medium text-san">发送给：{{ h.recipient_name || '调查员' }}</p>
                <p class="mt-1 whitespace-pre-wrap text-sm leading-relaxed text-fg-muted">{{ h.content }}</p>
              </article>
            </div>
          </section>

          <section v-if="data.truth.endings.length" class="space-y-2.5">
            <h3 class="text-sm font-semibold text-fg">全部结局分支</h3>
            <div class="space-y-2">
              <article
                v-for="e in data.truth.endings"
                :key="e.name"
                class="rounded-lg border border-line bg-surface-2/60 p-3"
              >
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-medium text-fg">{{ e.name }}</span>
                  <StampBadge :tone="e.is_failure ? 'seal' : 'moss'">{{ e.is_failure ? '失败结局' : '通关分支' }}</StampBadge>
                  <span v-if="e.san_reward" class="text-xs text-san">SAN 奖励：{{ e.san_reward }}</span>
                </div>
                <p v-if="e.trigger" class="mt-1.5 text-xs text-fg-subtle">触发条件：{{ e.trigger }}</p>
                <p v-if="e.description" class="mt-1 whitespace-pre-wrap text-sm text-fg-muted">{{ e.description }}</p>
              </article>
            </div>
          </section>

          <section v-if="data.truth.clues.length" class="space-y-2.5">
            <h3 class="text-sm font-semibold text-fg">模组全量线索</h3>
            <div class="space-y-2">
              <article
                v-for="(c, idx) in data.truth.clues"
                :key="idx"
                class="rounded-lg border border-line bg-surface-2/60 p-3"
              >
                <div class="flex flex-wrap items-center gap-2 text-xs">
                  <StampBadge :tone="clueNatureTone(c.nature)">{{ clueNatureLabel(c.nature) }}</StampBadge>
                  <span v-if="c.source" class="text-fg-subtle">来源：{{ c.source }}</span>
                  <span v-if="c.skill_check" class="text-accent">检定：{{ c.skill_check }}</span>
                </div>
                <p class="mt-1.5 text-sm text-fg">{{ c.summary }}</p>
                <p v-if="c.on_success" class="mt-1 text-xs text-fg-muted">成功获得：{{ c.on_success }}</p>
              </article>
            </div>
          </section>

          <section v-if="data.truth.npcs.length" class="space-y-2.5">
            <h3 class="text-sm font-semibold text-fg">模组 NPC 档案</h3>
            <div class="grid gap-2 sm:grid-cols-2">
              <article
                v-for="n in data.truth.npcs"
                :key="n.name"
                class="rounded-lg border border-line bg-surface-2/60 p-3"
              >
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-medium text-fg">{{ n.name }}</span>
                  <span v-if="n.occupation" class="text-xs text-fg-subtle">{{ n.occupation }}</span>
                  <StampBadge v-if="n.attitude" :tone="attitudeTone(n.attitude)">{{ n.attitude }}</StampBadge>
                </div>
                <p v-if="n.description" class="mt-1.5 text-xs leading-relaxed text-fg-muted">{{ n.description }}</p>
              </article>
            </div>
          </section>
        </template>
      </div>
    </template>
  </BaseModal>
</template>
