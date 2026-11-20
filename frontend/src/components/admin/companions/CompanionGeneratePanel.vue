<script setup lang="ts">
import { Sparkles } from '@lucide/vue'
import { onBeforeUnmount, ref } from 'vue'
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

const count = ref(5)
const running = ref(false)
const logs = ref<string[]>([])
let ctrl: AbortController | null = null

// NOTE: 离开页签即关闭连接。后端在客户端断开后仍会继续生成并入库，这里只是不再接收进度，所以中断后不再弹 toast / 写日志
onBeforeUnmount(() => ctrl?.abort())

async function generate() {
  if (running.value) return
  const n = Number(count.value)
  if (!Number.isInteger(n) || n < 1 || n > MAX_COMPANION_GEN_COUNT) {
    ui.toast(`生成数量必须是 1-${MAX_COMPANION_GEN_COUNT} 之间的整数`, 'error')
    return
  }

  running.value = true
  logs.value = []
  const log = (line: string) => {
    logs.value.push(formatLogLine(line))
  }
  log('已提交生成请求，等待服务器响应…')

  const mine = new AbortController()
  ctrl = mine
  const handler = createCompanionGenHandler(log)
  try {
    await runJobSocket('/api/admin/companion-cards/generate/ws', { count: n }, handler.onEvent, { signal: mine.signal })
    if (mine.signal.aborted) return
    const outcome = summarizeCompanionGen(handler.batchDone)
    outcome.logs.forEach(log)
    ui.toast(outcome.toast.message, outcome.toast.type)
    if (outcome.reload) await props.reload()
  } catch (e) {
    if (mine.signal.aborted) return
    const msg = errMsg(e)
    log(msg || '生成失败')
    ui.toast(msg, 'error')
  } finally {
    running.value = false
    if (ctrl === mine) ctrl = null
  }
}
</script>

<template>
  <PaperCard class="space-y-3">
    <div class="min-w-0">
      <SectionHeading>批量生成</SectionHeading>
      <p class="mt-1 text-xs text-fg-subtle">
        让 AI 直接生成人物卡放进卡池：性别、年龄、职业随机，姓名自动取中文名。每张卡需要几分钟，后台最多同时生成 3 张；中途关闭页面不影响后台生成。
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
