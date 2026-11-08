<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import BaseButton from '@/components/base/BaseButton.vue'
import BaseInput from '@/components/base/BaseInput.vue'
import PaperCard from '@/components/base/PaperCard.vue'
import { safeRedirect } from '@/router'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const tab = ref<'login' | 'register'>('login')
const loading = ref(false)
const error = ref('')
const loginForm = reactive({ username: '', password: '' })
const regForm = reactive({ username: '', email: '', password: '', invite_code: '' })

async function submit() {
  loading.value = true
  error.value = ''
  try {
    if (tab.value === 'login') await auth.login(loginForm.username, loginForm.password)
    else await auth.register({ ...regForm })
    await router.replace(safeRedirect(route.query.redirect))
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

auth.loadPublicSettings()
</script>

<template>
  <div class="flex min-h-dvh items-center justify-center px-4">
    <PaperCard class="w-full max-w-sm !p-8">
      <header class="mb-8 text-center">
        <h1 class="font-type text-3xl font-bold tracking-[0.3em] text-seal">LLM-COC</h1>
        <p class="mt-2 font-serif text-xs text-ink-soft">克苏鲁神话 TRPG · AI 主持系统</p>
      </header>

      <div class="mb-5 flex gap-1" role="tablist">
        <button
          v-for="t in [{ key: 'login', label: '登 录' }, { key: 'register', label: '注 册' }] as const"
          :key="t.key"
          type="button"
          role="tab"
          :aria-selected="tab === t.key"
          class="flex-1 rounded-sm border py-1.5 font-serif text-sm transition"
          :class="tab === t.key ? 'border-seal text-seal' : 'border-transparent text-ink-faint hover:text-ink'"
          @click="((tab = t.key), (error = ''))"
        >
          {{ t.label }}
        </button>
      </div>

      <form class="space-y-3" @submit.prevent="submit">
        <template v-if="tab === 'login'">
          <BaseInput v-model="loginForm.username" placeholder="用户名" autocomplete="username" />
          <BaseInput v-model="loginForm.password" type="password" placeholder="密码" autocomplete="current-password" />
        </template>
        <template v-else>
          <BaseInput v-model="regForm.username" placeholder="用户名（3-50字符）" autocomplete="username" />
          <BaseInput v-model="regForm.email" type="email" placeholder="邮箱" autocomplete="email" />
          <BaseInput v-model="regForm.password" type="password" placeholder="密码（至少6位）" autocomplete="new-password" />
          <BaseInput v-if="auth.publicSettings.requireInviteCode" v-model="regForm.invite_code" placeholder="邀请码" />
        </template>
        <BaseButton type="submit" class="mt-1 w-full" :loading="loading">
          {{ loading ? (tab === 'login' ? '验证中…' : '注册中…') : tab === 'login' ? '进入克苏鲁世界' : '创建账号' }}
        </BaseButton>
      </form>

      <p v-if="error" class="mt-3 rounded-sm border border-seal/50 bg-seal/5 px-3 py-2 text-center text-sm text-seal" role="alert">
        {{ error }}
      </p>
    </PaperCard>
  </div>
</template>
