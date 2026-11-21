<script setup lang="ts">
import { Sparkles } from '@lucide/vue'
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { runJobSocket } from '@/api/jobSocket'
import { errMsg } from '@/components/admin/shared/errMsg'
import StreamLogBox from '@/components/admin/scenarios/StreamLogBox.vue'
import { formatLogLine } from '@/components/admin/scenarios/scenarioStream'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import FormField from '@/components/base/FormField.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { useUiStore } from '@/stores/ui'
import { MAX_COMPANION_GEN_COUNT, createCompanionGenHandler, summarizeCompanionGen } from './companionGenerate'

const props = defineProps<{ reload: () => Promise<void> }>()
const ui = useUiStore()

const GEN_PATH = '/api/admin/companion-cards/generate/ws'

const count = ref(5)
const running = ref(false)
const logs = ref<string[]>([])
let ctrl: AbortController | null = null

// NOTE: 离开页签即关闭连接。后端在客户端断开后仍会继续生成并入库，这里只是不再接收进度，所以中断后不再弹 toast / 写日志
onBeforeUnmount(() => ctrl?.abort())

// NOTE: 刷新页面后后端任务可能还在跑：进来先接上它，回放已有的进度，任务还在跑就继续收后续事件
onMounted(() => void follow({ attach: true }))

function log(line: string, at?: number) {
  logs.value.push(formatLogLine(line, at === undefined ? undefined : new Date(at)))
}

async function generate() {
  if (running.value) return
  const n = Number(count.value)
  if (!Number.isInteger(n) || n < 1 || n > MAX_COMPANION_GEN_COUNT) {
    ui.toast(`生成数量必须是 1-${MAX_COMPANION_GEN_COUNT} 之间的整数`, 'error')
    return
  }

  // NOTE: 自动接上的连接可能还在握手，不先关掉的话它迟到的快照会盖掉这次启动的状态
  ctrl?.abort()
  running.value = true
  logs.value = []
  log('已提交生成请求，等待服务器响应…')
  await follow({ count: n })
}

async function follow(request: { count: number } | { attach: true }) {
  const mine = new AbortController()
  ctrl = mine
  const handler = createCompanionGenHandler(log, {
    onSnapshot(snapshot) {
      logs.value = []
      running.value = snapshot.running
    },
  })
  try {
    await runJobSocket(GEN_PATH, request, handler.onEvent, { signal: mine.signal })
    if (mine.signal.aborted) return
    // NOTE: 接上时任务已经结束，快照只是回放上一批的记录：不弹提示，卡池页自己会加载
    if (handler.snapshot && !handler.snapshot.running) return
    const outcome = summarizeCompanionGen(handler.batchDone)
    outcome.logs.forEach((line) => log(line))
    ui.toast(outcome.toast.message, outcome.toast.type)
    if (outcome.reload) await props.reload()
  } catch (e) {
    if (mine.signal.aborted) return
    // NOTE: 进页面时的自动接上只是尽力而为：接不上不打扰用户，点「批量生成」仍会走正常的报错提示
    if ('attach' in request) return
    const msg = errMsg(e)
    log(msg || '生成失败')
    ui.toast(msg, 'error')
  } finally {
    if (ctrl === mine) {
      ctrl = null
      running.value = false
    }
  }
}
</script>

<template>
  <PaperCard class="space-y-3">
    <div class="min-w-0">
      <SectionHeading>批量生成</SectionHeading>
      <p class="mt-1 text-xs text-fg-subtle">
        让 AI 直接生成人物卡放进卡池：性别、年龄、职业随机，姓名自动取中文名。每张卡需要几分钟，后台最多同时生成 3 张；中途关闭或刷新页面不影响后台生成，重新打开会接上进度。
      </p>
    </div>
    <div class="flex flex-col gap-2 sm:flex-row sm:items-end">
      <FormField label="生成数量" class="sm:w-40">
        <BaseInput v-model.number="count" type="number" min="1" :max="MAX_COMPANION_GEN_COUNT" :disabled="running" />
      </FormField>
      <BaseButton class="shrink-0" :loading="running" @click="generate">
        <Sparkles v-if="!running" :size="16" :stroke-width="1.75" />
        {{ running ? '生成中…' : '批量生成' }}
      </BaseButton>
    </div>
    <StreamLogBox v-if="running || logs.length" title="生成进度" :lines="logs" :running="running" @clear="logs = []" />
  </PaperCard>
</template>
