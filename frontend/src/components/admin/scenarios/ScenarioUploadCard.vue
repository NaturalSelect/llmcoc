<script setup lang="ts">
import { ref } from 'vue'
import { postForm } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { useUiStore } from '@/stores/ui'
import { errorMessage } from './pagedList'

const props = defineProps<{ reload: () => Promise<void> }>()
const ui = useUiStore()

const input = ref<HTMLInputElement | null>(null)
const file = ref<File | null>(null)
const uploading = ref(false)

function onPicked(e: Event) {
  file.value = (e.target as HTMLInputElement | null)?.files?.[0] || null
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
    if (input.value) input.value.value = ''
    ui.toast('模组导入成功')
    await props.reload()
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  } finally {
    uploading.value = false
  }
}
</script>

<template>
  <PaperCard>
    <SectionHeading>上传模组 JSON</SectionHeading>
    <p class="mt-1 text-sm text-fg-muted">支持单个 JSON 文件上传。</p>
    <div class="mt-4 flex flex-col gap-3 sm:flex-row sm:items-center">
      <input
        ref="input"
        type="file"
        accept="application/json,.json"
        aria-label="选择模组 JSON 文件"
        class="block w-full min-w-0 rounded-lg border border-line bg-surface-2 px-3 py-2 text-sm text-fg file:mr-3 file:rounded-lg file:border-0 file:bg-surface-3 file:px-2 file:py-1 file:text-xs file:text-fg"
        @change="onPicked"
      />
      <BaseButton class="shrink-0" :disabled="!file" :loading="uploading" @click="upload">
        {{ uploading ? '导入中…' : '上传并导入' }}
      </BaseButton>
    </div>
  </PaperCard>
</template>
