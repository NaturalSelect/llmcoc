<script setup lang="ts">
import { Download } from '@lucide/vue'
import { getToken } from '@/api/token'
import BaseButton from '@/components/base/BaseButton.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { useUiStore } from '@/stores/ui'
import { errorMessage } from './pagedList'

const ui = useUiStore()

// NOTE: 模板是二进制下载，api() 只会按 JSON 解析，所以这里直接 fetch 再用 a[download] 触发保存
async function download() {
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
    ui.toast(errorMessage(e), 'error')
  }
}
</script>

<template>
  <PaperCard>
    <div class="flex items-center justify-between gap-4">
      <div class="min-w-0">
        <SectionHeading>下载模组模板</SectionHeading>
        <p class="mt-1 text-sm text-fg-muted">下载标准 JSON 模板，填写后可用于导入模组。</p>
      </div>
      <BaseButton variant="secondary" class="shrink-0" @click="download">
        <Download :size="16" :stroke-width="1.75" />
        下载模板
      </BaseButton>
    </div>
  </PaperCard>
</template>
