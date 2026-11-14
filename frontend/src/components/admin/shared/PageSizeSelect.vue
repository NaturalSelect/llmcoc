<script setup lang="ts">
import { useId } from 'vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import { PAGE_SIZES } from './pagedList'

const model = defineModel<number>({ required: true })
const id = useId()
</script>

<template>
  <label :for="id" class="text-xs text-fg-subtle">每页</label>
  <!-- NOTE: BaseSelect 没有 size 属性，用外层容器把高度压到与 sm 按钮一致（h-8） -->
  <div class="w-20 [&_select]:h-8">
    <BaseSelect :id="id" :model-value="model" @update:model-value="model = Number($event)">
      <option v-for="n in PAGE_SIZES" :key="n" :value="n">{{ n }}</option>
    </BaseSelect>
  </div>
</template>
