<script setup lang="ts">
import { ChevronDown, Lock } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { JoinSessionForm } from '@/api/sessionTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'
import { buildJoinPayload, cardOptionLabel, emptyJoinForm, errorMessage, joinableCharacters } from './sessionLogic'

// NOTE: 大厅卡片（LobbySession）和游戏页里的房间（Session）都要用，所以只声明用到的三个字段
const props = defineProps<{ open: boolean; session: { id: number; name?: string; has_password?: boolean } | null }>()
const emit = defineEmits<{ close: []; joined: [] }>()

const chars = useCharactersStore()
const ui = useUiStore()

const form = ref<JoinSessionForm>(emptyJoinForm())
const secretInput = ref('')
const showSecret = ref(false)
const joining = ref(false)
const candidates = computed(() => joinableCharacters(chars.characters))

// 每次打开都清空上次选择的人物卡和密码；只有一张可用卡时直接选中
watch(
  () => props.open,
  (open) => {
    if (!open) return
    form.value = emptyJoinForm()
    secretInput.value = ''
    showSecret.value = false
    const only = candidates.value.length === 1 ? candidates.value[0] : undefined
    if (only?.id) form.value.character_card_id = only.id
  },
  { immediate: true },
)

async function submit() {
  const payload = buildJoinPayload({ ...form.value, secret_background: secretInput.value })
  if (!props.session || !payload) return
  joining.value = true
  try {
    await api('POST', `/api/sessions/${props.session.id}/join`, payload)
    emit('close')
    ui.toast('加入成功！')
    emit('joined')
  } catch (e) {
    ui.toast(errorMessage(e), 'error')
  } finally {
    joining.value = false
  }
}
</script>

<template>
  <BaseModal :open="open" title="加入房间" max-width="max-w-sm" @close="emit('close')">
    <p class="mb-4 text-sm text-fg-muted">{{ session?.name }}</p>
    <form class="space-y-3" @submit.prevent="submit">
      <BaseSelect v-model="form.character_card_id" aria-label="选择携带的人物卡">
        <option value="">选择携带的人物卡 *</option>
        <option v-for="c in candidates" :key="c.id" :value="c.id">{{ cardOptionLabel(c) }}</option>
      </BaseSelect>
      <BaseInput v-if="session?.has_password" v-model="form.password" type="text" placeholder="房间密码" aria-label="房间密码" autocomplete="off" />

      <div class="rounded-lg border border-line bg-surface-2/60 px-3 py-2">
        <button
          type="button"
          class="flex w-full items-center justify-between gap-2 text-left text-xs font-medium text-fg-muted transition hover:text-fg"
          :aria-expanded="showSecret"
          @click="showSecret = !showSecret"
        >
          <span class="inline-flex items-center gap-1.5">
            <Lock :size="14" :stroke-width="1.75" class="shrink-0 text-info" />
            秘密背景 / 隐藏任务 (HO，可选)
          </span>
          <ChevronDown :size="14" :stroke-width="1.75" class="shrink-0 transition-transform" :class="showSecret ? 'rotate-180' : ''" />
        </button>
        <div v-if="showSecret" class="mt-2 space-y-1.5">
          <textarea
            v-model="secretInput"
            rows="3"
            maxlength="1000"
            placeholder="仅你与 KP 可见：例如隐藏身份、不可告人的目的或模组 HO 内容…"
            aria-label="秘密背景或隐藏任务"
            class="w-full resize-none rounded-lg border border-line bg-surface px-2.5 py-2 text-xs leading-relaxed text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
          />
          <p class="text-right text-xs tabular-nums text-fg-subtle">{{ secretInput.length }}/1000</p>
        </div>
      </div>

      <div class="flex gap-2 pt-1">
        <BaseButton variant="ghost" class="flex-1" @click="emit('close')">取消</BaseButton>
        <BaseButton type="submit" class="flex-1" :loading="joining" :disabled="!form.character_card_id">
          {{ joining ? '加入中…' : '加入' }}
        </BaseButton>
      </div>
    </form>
  </BaseModal>
</template>
