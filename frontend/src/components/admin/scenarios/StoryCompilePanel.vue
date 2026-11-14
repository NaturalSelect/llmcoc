<script setup lang="ts">
import { onBeforeUnmount, reactive, ref } from 'vue'
import { runJobSocket } from '@/api/jobSocket'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseFileInput from '@/components/base/BaseFileInput.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import FormField from '@/components/base/FormField.vue'
import { useUiStore } from '@/stores/ui'
import { errMsg } from '@/components/admin/shared/errMsg'
import { createCompileHandler, formatLogLine, summarizeCompile, validateStoryFile } from './scenarioStream'
import StreamLogBox from './StreamLogBox.vue'

const props = defineProps<{ reload: () => Promise<void> }>()
const ui = useUiStore()

const fileInput = ref<InstanceType<typeof BaseFileInput> | null>(null)
const form = reactive({ name: '' })
const running = ref(false)
const logs = ref<string[]>([])
let controller: AbortController | null = null

// NOTE: 离开页签即关闭在途编译连接；后端会继续编译并入库，只是这里不再接收进度
onBeforeUnmount(() => controller?.abort())

// NOTE: 故事编译不支持批量，一次只跑一篇；神话锚点与奖励概念由后端从文档内容自动识别，前端只负责上传文件
async function compile() {
  const file = fileInput.value?.file()
  if (!file) {
    ui.toast('请选择一个 .md 文件', 'error')
    return
  }
  const invalid = validateStoryFile(file)
  if (invalid) {
    ui.toast(invalid, 'error')
    return
  }
  if (running.value) return

  let storyDocument: string
  try {
    storyDocument = await file.text()
  } catch {
    ui.toast('文件读取失败，请检查文件是否有效', 'error')
    return
  }
  if (!storyDocument.trim()) {
    ui.toast('文件内容为空', 'error')
    return
  }

  running.value = true
  logs.value = []
  const log = (line: string) => {
    logs.value.push(formatLogLine(line))
  }
  log('已提交编译请求，等待服务器响应…')

  const ctrl = new AbortController()
  controller = ctrl
  const handler = createCompileHandler(log)
  try {
    await runJobSocket(
      '/api/scenarios/compile-story/ws',
      { story_document: storyDocument, name: form.name || '' },
      handler.onEvent,
      { signal: ctrl.signal },
    )
    if (ctrl.signal.aborted) return
    const outcome = summarizeCompile(handler.done, handler.error)
    outcome.logs.forEach(log)
    ui.toast(outcome.toast.message, outcome.toast.type)
    if (outcome.reload) await props.reload()
  } catch (e) {
    if (ctrl.signal.aborted) return
    const msg = errMsg(e)
    log(msg || '编译失败')
    ui.toast(msg, 'error')
  } finally {
    if (controller === ctrl) controller = null
    running.value = false
  }
}
</script>

<template>
  <div>
    <p class="mb-4 text-sm text-fg-muted">上传 Markdown 故事，跳过 AI 创作直接编译入库；神话锚点和奖励由系统识别。中途关闭页面不影响后台编译。</p>
    <div class="mb-3">
      <FormField label="故事文档" hint=".md / .markdown / .txt，≤500KB；场景、NPC、线索写得越清楚，编译越准确">
        <BaseFileInput ref="fileInput" accept=".md,.markdown,.txt,text/markdown,text/plain" />
      </FormField>
    </div>
    <div class="mb-3">
      <FormField label="模组名称（可选）">
        <BaseInput v-model="form.name" type="text" placeholder="留空由系统命名" />
      </FormField>
    </div>
    <BaseButton :loading="running" @click="compile">{{ running ? '编译中…' : '开始编译' }}</BaseButton>
    <StreamLogBox v-if="running || logs.length" title="编译进度" :lines="logs" :running="running" @clear="logs = []" />
  </div>
</template>
