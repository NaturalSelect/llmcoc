<script setup lang="ts">
import { reactive, ref } from 'vue'
import { BookOpen } from '@lucide/vue'
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
  <div class="relative flex min-h-dvh items-center justify-center overflow-hidden bg-bg px-4">
    <div class="pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_50%_0%,rgb(217_165_74/0.12),transparent_60%)]" aria-hidden="true" />
    <PaperCard padding="lg" class="relative w-full max-w-sm">
      <header class="mb-6 text-center">
        <div class="mx-auto mb-3 flex h-11 w-11 items-center justify-center rounded-xl bg-accent/15 text-accent">
          <BookOpen :size="22" :stroke-width="1.75" />
        </div>
        <h1 class="text-xl font-semibold tracking-wide text-fg">LLM-COC</h1>
        <p class="mt-1.5 text-sm text-fg-muted">克苏鲁神话 TRPG · AI 主持系统</p>
      </header>

      <div class="mb-5 grid grid-cols-2 gap-1 rounded-lg bg-surface-2 p-1" role="tablist">
        <button
          v-for="t in [{ key: 'login', label: '登 录' }, { key: 'register', label: '注 册' }] as const"
          :key="t.key"
          type="button"
          role="tab"
          :aria-selected="tab === t.key"
          class="rounded-md py-1.5 text-sm font-medium transition"
          :class="tab === t.key ? 'bg-surface-3 text-fg' : 'text-fg-subtle hover:text-fg-muted'"
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

      <p v-if="error" class="mt-3 rounded-lg border border-danger/40 bg-danger/10 px-3 py-2 text-center text-sm text-danger" role="alert">
        {{ error }}
      </p>
    </PaperCard>
  </div>
</template>
