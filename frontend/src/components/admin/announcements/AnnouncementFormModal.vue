<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { Announcement } from '@/api/adminTypesContent'
import { api } from '@/api/client'
import { errorMessage } from '@/components/admin/scenarios/pagedList'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import BaseTextarea from '@/components/base/BaseTextarea.vue'
import FormField from '@/components/base/FormField.vue'
import { useUiStore } from '@/stores/ui'
import { renderMarkdown } from '@/utils/markdown'
import {
  emptyAnnouncementForm,
  formFromAnnouncement,
  LEVEL_OPTIONS,
  toAnnouncementPayload,
} from './announcementLogic'

const props = defineProps<{ open: boolean; editing: Announcement | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const ui = useUiStore()

const form = reactive(emptyAnnouncementForm())
const saving = ref(false)
const preview = computed(() => renderMarkdown(form.content))

// NOTE: 弹窗组件常驻挂载，每次打开都要按「新建 / 编辑」重置表单，否则会残留上一次的输入
watch(
  () => props.open,
  (open) => {
    if (open) Object.assign(form, props.editing ? formFromAnnouncement(props.editing) : emptyAnnouncementForm())
  },
  { immediate: true },
)

async function save() {
  if (!form.title.trim()) {
    ui.toast('标题不能为空', 'error')
    return
  }
  const payload = toAnnouncementPayload(form)
  saving.value = true
  try {
    if (props.editing) {
      await api('PUT', '/api/admin/announcements/' + props.editing.id, payload)
      ui.toast('公告已更新')
    } else {
      await api('POST', '/api/admin/announcements', payload)
      ui.toast('公告已创建')
    }
    emit('saved')
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <BaseModal :open="open" :title="editing ? '编辑公告' : '新建公告'" max-width="max-w-2xl" @close="emit('close')">
    <form class="space-y-3" @submit.prevent="save">
      <BaseInput v-model="form.title" placeholder="标题 *" aria-label="标题" />
      <BaseSelect v-model="form.level" aria-label="公告等级">
        <option v-for="o in LEVEL_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
      </BaseSelect>
      <BaseTextarea
        v-model="form.content"
        placeholder="支持 Markdown 语法"
        rows="8"
        aria-label="公告内容"
      />
      <div>
        <div class="mb-1.5 text-sm text-fg-muted">预览</div>
        <!-- NOTE: renderMarkdown 经过 DOMPurify 清洗，是唯一允许 v-html 的来源 -->
        <div
          class="announcement-preview max-h-40 overflow-y-auto rounded-lg bg-surface-2 p-3 text-sm"
          v-html="preview"
        />
      </div>
      <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
        <FormField label="开始时间（留空立即生效）">
          <BaseInput v-model="form.starts_at" type="datetime-local" />
        </FormField>
        <FormField label="结束时间（留空永不过期）">
          <BaseInput v-model="form.ends_at" type="datetime-local" />
        </FormField>
      </div>
      <div class="flex items-center gap-4">
        <label class="flex cursor-pointer items-center gap-2 text-sm text-fg-muted">
          <input v-model="form.is_pinned" type="checkbox" class="accent-accent" /> 置顶
        </label>
        <label class="flex cursor-pointer items-center gap-2 text-sm text-fg-muted">
          <input v-model="form.is_active" type="checkbox" class="accent-accent" /> 启用
        </label>
      </div>
      <div class="flex justify-end gap-2 pt-1">
        <BaseButton variant="ghost" @click="emit('close')">取消</BaseButton>
        <BaseButton type="submit" :loading="saving">保存</BaseButton>
      </div>
    </form>
  </BaseModal>
</template>
