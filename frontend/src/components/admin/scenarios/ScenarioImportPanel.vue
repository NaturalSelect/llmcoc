<script setup lang="ts">
import { Download } from '@lucide/vue'
import { ref } from 'vue'
import { postForm } from '@/api/client'
import { getToken } from '@/api/token'
import { errMsg } from '@/components/admin/shared/errMsg'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseFileInput from '@/components/base/BaseFileInput.vue'
import { useUiStore } from '@/stores/ui'

const props = defineProps<{ reload: () => Promise<void> }>()
const ui = useUiStore()

const input = ref<InstanceType<typeof BaseFileInput> | null>(null)
const file = ref<File | null>(null)
const uploading = ref(false)

function onPicked() {
  file.value = input.value?.file() ?? null
}

// NOTE: 模板是二进制下载，api() 只会按 JSON 解析，所以这里直接 fetch 再用 a[download] 触发保存
async function downloadTemplate() {
  try {
    const token = getToken()
    const resp = await fetch('/api/scenarios/template', {
      method: 'GET',
      headers: token ? { Authorization: 'Bearer ' + token } : {},
    })
    if (!resp.ok) {
      const data = await resp.json().catch(() => ({}))
      throw new Error(data.error || `HTTP ${resp.status}`)
    }
    const blob = await resp.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'scenario_template.json'
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
    ui.toast('模板下载成功')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  }
}

// NOTE: 上传走 multipart，api() 只发 JSON，所以用 postForm；它不因状态码抛错，这里按旧实现自行判断
async function upload() {
  if (!file.value) return
  uploading.value = true
  try {
    const form = new FormData()
    form.append('file', file.value)
    const { status, data } = await postForm('/api/scenarios/upload', form)
    if (status < 200 || status >= 300) throw new Error(data?.error || `HTTP ${status}`)
    file.value = null
    // NOTE: 同步清空输入框，否则页面上仍显示已上传的文件名，而按钮因 file 为空处于禁用状态
    input.value?.clear()
    ui.toast('模组导入成功')
    await props.reload()
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    uploading.value = false
  }
}
</script>

<template>
  <div>
    <div class="mb-4 flex items-center justify-between gap-4">
      <p class="min-w-0 text-sm text-fg-muted">按标准 JSON 模板填写后上传导入，支持单个文件。</p>
      <BaseButton variant="secondary" size="sm" class="shrink-0" @click="downloadTemplate">
        <Download :size="16" :stroke-width="1.75" />
        下载模板
      </BaseButton>
    </div>
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center">
      <BaseFileInput ref="input" accept="application/json,.json" aria-label="选择模组 JSON 文件" @change="onPicked" />
      <BaseButton class="shrink-0" :disabled="!file" :loading="uploading" @click="upload">
        {{ uploading ? '导入中…' : '上传并导入' }}
      </BaseButton>
    </div>
  </div>
</template>
