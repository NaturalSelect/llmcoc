<script setup lang="ts">
import { reactive, ref } from 'vue'
import type { RechargeForm, RechargeResult } from '@/api/adminTypesCore'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import { useUiStore } from '@/stores/ui'
import { errMsg } from '../shared/errMsg'

const emit = defineEmits<{ recharged: [] }>()
const ui = useUiStore()

// NOTE: 与旧前端一致：数字框清空后是空串，原样提交，由后端校验并返回错误文案；成功后不清空表单
const form = reactive<RechargeForm>({ user_id: '', amount: 100, note: '' })
const busy = ref(false)

async function submit() {
  busy.value = true
  try {
    const r = await api<RechargeResult>('POST', '/api/admin/recharge', { ...form })
    ui.toast(`充值成功：${r.user.username} +${form.amount} 金币`)
    emit('recharged')
  } catch (e) {
    ui.toast(errMsg(e), 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <PaperCard tag="section" aria-labelledby="recharge-title" class="self-start">
    <h3 id="recharge-title" class="mb-4 border-b border-paper-edge pb-2 font-type text-sm font-bold tracking-widest text-seal">充值金币</h3>
    <div class="space-y-2.5">
      <BaseInput v-model.number="form.user_id" type="number" placeholder="用户 ID" aria-label="用户 ID" />
      <BaseInput v-model.number="form.amount" type="number" placeholder="充值数量" aria-label="充值数量" />
      <BaseInput v-model="form.note" placeholder="备注（可选）" aria-label="备注" />
      <BaseButton class="w-full" :loading="busy" @click="submit">确认充值</BaseButton>
    </div>
  </PaperCard>
</template>
