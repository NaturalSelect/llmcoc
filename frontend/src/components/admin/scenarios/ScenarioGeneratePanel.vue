<script setup lang="ts">
import { onBeforeUnmount, reactive, ref } from 'vue'
import type { ScenarioGenForm } from '@/api/adminTypesContent'
import { streamSSE } from '@/api/sse'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import BaseTextarea from '@/components/base/BaseTextarea.vue'
import FormField from '@/components/base/FormField.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import { useUiStore } from '@/stores/ui'
import { errorMessage } from './pagedList'
import { createGenerationHandler, formatLogLine, summarizeBatchDone } from './scenarioStream'
import StreamLogBox from './StreamLogBox.vue'

const props = defineProps<{ reload: () => Promise<void> }>()
const ui = useUiStore()

const form = reactive<ScenarioGenForm>({
  name: '',
  theme: '',
  era: '',
  brief: '',
  target_length: 'short',
  min_players: 1,
  max_players: 4,
  difficulty: 'normal',
  count: 1,
})
const activeCount = ref(0)
const logs = ref<string[]>([])
const controllers = new Set<AbortController>()

// NOTE: 离开页签即中断所有在途流。后端在客户端断开后仍会继续生成并入库（见 GenerateScenarioByAgents），
// 这里只是不再接收进度，所以中断后不再弹 toast / 写日志
onBeforeUnmount(() => {
  for (const c of controllers) c.abort()
  controllers.clear()
})

// NOTE: 按钮不阻塞：生成进行中仍可再次提交新任务（后端按全局槽位排队），因此不做互斥；
// 仅在没有任务运行时才清空日志，避免打断正在输出的历史记录
async function generate() {
  const minPlayers = Number(form.min_players || 1)
  const maxPlayers = Number(form.max_players || 4)
  if (maxPlayers < minPlayers) {
    ui.toast('最大玩家数不能小于最小玩家数', 'error')
    return
  }
  const count = Number(form.count || 1)

  if (activeCount.value === 0) logs.value = []
  activeCount.value++
  const log = (line: string) => {
    logs.value.push(formatLogLine(line))
  }
  log('已提交生成请求，等待服务器响应…')

  const ctrl = new AbortController()
  controllers.add(ctrl)
  const handler = createGenerationHandler(log)
  try {
    await streamSSE(
      '/api/scenarios/generate',
      {
        name: form.name || '',
        theme: form.theme || '',
        era: form.era || '',
        brief: form.brief || '',
        target_length: form.target_length || 'short',
        min_players: minPlayers,
        max_players: maxPlayers,
        difficulty: form.difficulty || 'normal',
        count,
      },
      handler.onEvent,
      { signal: ctrl.signal },
    )
    if (ctrl.signal.aborted) return
    const outcome = summarizeBatchDone(handler.batchDone)
    outcome.logs.forEach(log)
    ui.toast(outcome.toast.message, outcome.toast.type)
    if (outcome.reload) await props.reload()
  } catch (e) {
    if (ctrl.signal.aborted) return
    const msg = errorMessage(e)
    log('❌ ' + (msg || '生成失败'))
    ui.toast(msg, 'error')
  } finally {
    controllers.delete(ctrl)
    activeCount.value--
  }
}
</script>

<template>
  <PaperCard class="lg:col-span-2">
    <h3 class="mb-4 font-type text-sm font-bold tracking-wide text-seal">AI 生成模组</h3>
    <p class="mb-3 text-xs text-ink-faint">
      流水线：约束构建 → 正文生成（多轮工具调用）→ 自动修复 → QA/逻辑审查 → 奖励设计。生成过程流式显示，耗时数分钟；中途关闭页面不影响后台生成与入库。
    </p>
    <div class="mb-3 grid grid-cols-1 gap-3 md:grid-cols-2">
      <FormField label="模组名称（可选）" hint="留空则AI自动命名。">
        <BaseInput v-model="form.name" type="text" placeholder="留空由AI命名" />
      </FormField>
      <FormField label="主题 / 风格" hint="引导AI的神话题材与氛围。">
        <BaseInput v-model="form.theme" type="text" placeholder="例如：海洋、都市传说、古埃及" />
      </FormField>
      <FormField label="时代背景" hint="年代地点，影响NPC和道具。">
        <BaseInput v-model="form.era" type="text" placeholder="例如：1920s 波士顿、现代东京" />
      </FormField>
      <FormField label="篇幅" hint="影响场景数量和线索密度。">
        <BaseSelect v-model="form.target_length">
          <option value="short">短篇（剧情1-3天）</option>
          <option value="mid">中篇（剧情3-7天）</option>
          <option value="long">长篇（剧情7-30天）</option>
          <option value="random">random（随机 · 从短篇/中篇/长篇中随机选择）</option>
        </BaseSelect>
      </FormField>
    </div>
    <div class="mb-3">
      <FormField label="创意简报（可选）" hint="一两句话描述核心剧情或特殊要求。">
        <BaseTextarea
          v-model="form.brief"
          rows="2"
          class="resize-none"
          placeholder="例如：调查员受雇寻找失踪的渔夫，背后隐藏着古老渔村的祭祀秘密…"
        />
      </FormField>
    </div>
    <div class="mb-3 grid grid-cols-1 gap-3 md:grid-cols-4">
      <FormField label="最小玩家数" hint="建议能开团的最低人数。">
        <BaseInput v-model.number="form.min_players" type="number" min="1" placeholder="例如：1" />
      </FormField>
      <FormField label="最大玩家数" hint="建议不影响节奏的上限人数。">
        <BaseInput v-model.number="form.max_players" type="number" min="1" placeholder="例如：4" />
      </FormField>
      <FormField label="难度等级" hint="决定威胁主体的规模与神话位阶，同时影响调查节奏与代价。">
        <BaseSelect v-model="form.difficulty">
          <option value="easy">easy（简单 · 下级神话生物 / 邪教徒 / 人类巫师）</option>
          <option value="normal">normal（普通 · 上级神话生物 / 多种下级生物）</option>
          <option value="hard">hard（困难 · 大型邪教 / 旧日苏醒 / 旧日间接干涉）</option>
          <option value="random">random（随机 · 从简单/普通/困难中随机选择）</option>
        </BaseSelect>
      </FormField>
      <FormField label="生成数量" hint="批量生成多个模组（1-10），复用同一套参数，后台按顺序逐个生成。">
        <BaseInput v-model.number="form.count" type="number" min="1" max="10" placeholder="1" />
      </FormField>
    </div>

    <BaseButton @click="generate">
      {{ Number(form.count) > 1 ? '批量生成 ' + form.count + ' 个并入库' : '生成并入库' }}
    </BaseButton>
    <p v-if="activeCount > 0" class="mt-1 text-[11px] text-seal">
      ● 后台有 {{ activeCount }} 个生成任务运行/排队中，可继续调整参数或再次提交，无需等待。
    </p>
    <StreamLogBox
      v-if="activeCount > 0 || logs.length"
      title="生成进度"
      :lines="logs"
      :running="activeCount > 0"
      @clear="logs = []"
    />
  </PaperCard>
</template>
