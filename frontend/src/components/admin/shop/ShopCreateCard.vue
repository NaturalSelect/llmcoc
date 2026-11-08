<script setup lang="ts">
import { reactive } from 'vue'
import { api } from '@/api/client'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import BaseTextarea from '@/components/base/BaseTextarea.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import { errorMessage } from '@/components/admin/scenarios/pagedList'
import { useUiStore } from '@/stores/ui'
import { emptyShopItemForm, SHOP_ITEM_TYPES } from './shopForm'

const props = defineProps<{ created: () => Promise<void> }>()
const ui = useUiStore()

const form = reactive(emptyShopItemForm())

// NOTE: 名称为空时旧实现静默返回，不弹提示；价格/效果值清空后按原样提交，由后端校验报错
async function create() {
  if (!form.name) return
  try {
    await api('POST', '/api/admin/shop/items', form)
    ui.toast('商品已创建')
    Object.assign(form, emptyShopItemForm())
    await props.created()
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  }
}
</script>

<template>
  <PaperCard>
    <h3 class="mb-4 font-type text-sm font-bold tracking-wide text-seal">创建商品</h3>
    <form class="space-y-2.5" @submit.prevent="create">
      <BaseInput v-model="form.name" placeholder="商品名称 *" aria-label="商品名称" />
      <BaseTextarea v-model="form.description" placeholder="商品描述" rows="2" aria-label="商品描述" class="resize-none" />
      <BaseSelect v-model="form.item_type" aria-label="商品类型">
        <option v-for="t in SHOP_ITEM_TYPES" :key="t.value" :value="t.value">{{ t.label }}</option>
      </BaseSelect>
      <div class="grid grid-cols-2 gap-2">
        <BaseInput v-model.number="form.price" type="number" placeholder="价格（金币）" aria-label="价格（金币）" />
        <BaseInput v-model.number="form.value" type="number" placeholder="效果值" aria-label="效果值" />
      </div>
      <BaseButton type="submit" class="w-full">创建商品</BaseButton>
    </form>
  </PaperCard>
</template>
