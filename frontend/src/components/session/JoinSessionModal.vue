<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { JoinSessionForm, LobbySession } from '@/api/sessionTypes'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import BaseModal from '@/components/base/BaseModal.vue'
import BaseSelect from '@/components/base/BaseSelect.vue'
import { useCharactersStore } from '@/stores/characters'
import { useUiStore } from '@/stores/ui'
import { buildJoinPayload, cardOptionLabel, emptyJoinForm, errorMessage, joinableCharacters } from './sessionLogic'

const props = defineProps<{ open: boolean; session: LobbySession | null }>()
const emit = defineEmits<{ close: []; joined: [] }>()

const chars = useCharactersStore()
const ui = useUiStore()

const form = ref<JoinSessionForm>(emptyJoinForm())
const joining = ref(false)
const candidates = computed(() => joinableCharacters(chars.characters))

// 每次打开都清空上次选择的人物卡和密码
watch(
  () => props.open,
  (open) => {
    if (open) form.value = emptyJoinForm()
  },
  { immediate: true },
)

async function submit() {
  const payload = buildJoinPayload(form.value)
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
    <p class="mb-4 font-type text-xs text-ink-soft">{{ session?.name }}</p>
    <form class="space-y-3" @submit.prevent="submit">
      <BaseSelect v-model="form.character_card_id" aria-label="选择携带的人物卡">
        <option value="">选择携带的人物卡 *</option>
        <option v-for="c in candidates" :key="c.id" :value="c.id">{{ cardOptionLabel(c) }}</option>
      </BaseSelect>
      <BaseInput v-if="session?.has_password" v-model="form.password" type="text" placeholder="房间密码" aria-label="房间密码" autocomplete="off" />
      <div class="flex gap-2 pt-1">
        <BaseButton variant="ghost" class="flex-1" @click="emit('close')">取消</BaseButton>
        <BaseButton type="submit" class="flex-1" :loading="joining" :disabled="!form.character_card_id">
          {{ joining ? '加入中…' : '加入' }}
        </BaseButton>
      </div>
    </form>
  </BaseModal>
</template>
