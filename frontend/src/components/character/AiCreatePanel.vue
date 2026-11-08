<script setup lang="ts">
import { reactive, ref } from 'vue'
import { Sparkles } from '@lucide/vue'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import BaseTextarea from '@/components/base/BaseTextarea.vue'
import FormField from '@/components/base/FormField.vue'
import { useUiStore } from '@/stores/ui'
import { characterApi } from './characterApi'
import { errMessage } from './characterLogic'
import { registerCreatedCard } from './createdCard'
import { MAX_AGE, MIN_AGE, aiPayload, emptyAiForm, type AiForm } from './manualCreate'

const emit = defineEmits<{ close: [] }>()
const ui = useUiStore()

const form = reactive<AiForm>(emptyAiForm())
const loading = ref(false)

async function generate(): Promise<void> {
  loading.value = true
  try {
    const card = await characterApi.generate(aiPayload(form))
    emit('close')
    await registerCreatedCard(card, 'AI 调查员生成成功！')
  } catch (e) {
    ui.toast(errMessage(e), 'error')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-md space-y-4" role="tabpanel">
    <BaseInput v-model="form.name" placeholder="姓名 *" aria-label="姓名" />
    <div class="grid grid-cols-2 gap-3">
      <FormField label="性别">
        <BaseSelect v-model="form.gender">
          <option value="">请指定</option>
          <option value="男">男</option>
          <option value="女">女</option>
        </BaseSelect>
      </FormField>
      <FormField label="年龄">
        <BaseInput v-model.number="form.age" type="number" :min="MIN_AGE" :max="MAX_AGE" placeholder="如：28" />
      </FormField>
    </div>
    <BaseInput v-model="form.occupation" placeholder="职业（如：记者、医生、考古学家）" aria-label="职业" />
    <BaseInput v-model="form.era" placeholder="时代（默认：1920年代）" aria-label="时代" />
    <BaseTextarea v-model="form.background" rows="3" placeholder="背景提示（可选）" aria-label="背景提示" />
    <div class="flex gap-2 pt-1">
      <BaseButton variant="ghost" class="flex-1" @click="emit('close')">取消</BaseButton>
      <BaseButton class="flex-1" :loading="loading" :disabled="!form.name.trim()" @click="generate">
        <Sparkles v-if="!loading" :size="16" :stroke-width="1.75" />
        {{ loading ? '生成中…' : 'AI 生成' }}
      </BaseButton>
    </div>
  </div>
</template>
