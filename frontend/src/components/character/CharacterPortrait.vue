<script setup lang="ts">
import { ImagePlus, RefreshCw, UserRound } from '@lucide/vue'
import { computed } from 'vue'
import BaseButton from '@/components/base/BaseButton.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import { portraitTitle } from './characterLogic'

const props = defineProps<{ src: string; cost: number; coins: number; busy: boolean }>()
const emit = defineEmits<{ generate: []; preview: [] }>()

const regenerate = computed(() => !!props.src)
</script>

<template>
  <section class="mb-5 flex items-start gap-4">
    <button
      v-if="src"
      type="button"
      class="shrink-0 rounded-lg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      aria-label="查看立绘大图"
      @click="emit('preview')"
    >
      <img :src="src" alt="调查员立绘" class="aspect-[2/3] w-32 cursor-zoom-in rounded-lg bg-surface-2 object-cover" />
    </button>
    <div v-else class="flex aspect-[2/3] w-32 shrink-0 flex-col items-center justify-center gap-2 rounded-lg bg-surface-2 text-fg-subtle">
      <UserRound :size="32" :stroke-width="1.5" />
      <span class="text-xs">尚未生成立绘</span>
    </div>
    <div class="min-w-0 flex-1 space-y-2">
      <SectionHeading>立绘</SectionHeading>
      <p class="text-xs text-fg-subtle">每次生成花费 {{ cost }} 金币。立绘依据外貌和当前穿戴的装备绘制，换装后不会自动更新。</p>
      <BaseButton size="sm" variant="secondary" :loading="busy" :disabled="busy || coins < cost" :title="portraitTitle(cost, coins, regenerate)" @click="emit('generate')">
        <template v-if="busy">生成中…</template>
        <template v-else-if="regenerate">
          <RefreshCw :size="16" :stroke-width="1.75" />
          重新生成立绘
        </template>
        <template v-else>
          <ImagePlus :size="16" :stroke-width="1.75" />
          生成立绘
        </template>
      </BaseButton>
    </div>
  </section>
</template>
