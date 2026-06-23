<template>
  <div style="display: flex; align-items: center; justify-content: center; min-height: 100vh; background: #0F172A;">
    <n-card style="width: 400px;" :bordered="true">
      <template #header>
        <div style="text-align: center;">
          <img src="/logo.svg" alt="gohookbridge" style="height: 36px; margin-bottom: 8px;" />
          <n-text depth="3">Sign in to continue</n-text>
        </div>
      </template>

      <n-alert v-if="errorMsg" type="error" closable @close="errorMsg = ''" style="margin-bottom: 16px;">
        {{ errorMsg }}
      </n-alert>

      <template v-if="oidcProviders.length > 0">
        <n-space vertical>
          <n-button
            v-for="p in oidcProviders"
            :key="p.id"
            block
            secondary
            @click="oidcLogin(p.id)"
          >
            <template #icon><n-icon><svg viewBox="0 0 24 24"><path fill="currentColor" d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/></svg></n-icon></template>
            Login with {{ p.name }}
          </n-button>
        </n-space>
        <n-divider v-if="localEnabled">or</n-divider>
      </template>

      <template v-if="localEnabled">
        <n-form @submit.prevent="handleLogin">
          <n-form-item label="Username">
            <n-input v-model:value="username" autocomplete="username" />
          </n-form-item>
          <n-form-item label="Password">
            <n-input v-model:value="password" type="password" autocomplete="current-password" />
          </n-form-item>
          <n-button type="primary" block attr-type="submit" :loading="loading">Sign In</n-button>
        </n-form>
      </template>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { NCard, NButton, NInput, NForm, NFormItem, NDivider, NText, NIcon, NSpace, NAlert } from 'naive-ui'
import { useAuthStore } from '../stores/auth'
import { api } from '../api/client'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const username = ref('')
const password = ref('')
const loading = ref(false)
const errorMsg = ref('')
const oidcProviders = ref<{ id: string; name: string }[]>([])
const localEnabled = ref(false)

onMounted(async () => {
  try {
    const methods = await api.getAuthMethods()
    oidcProviders.value = methods.oidc_providers || []
    localEnabled.value = methods.local_enabled
  } catch {
    oidcProviders.value = []
    localEnabled.value = true
  }
})

async function handleLogin() {
  loading.value = true
  errorMsg.value = ''
  try {
    await auth.login(username.value, password.value)
    const redirect = (route.query.redirect as string) || '/'
    router.push(redirect)
  } catch (e: any) {
    errorMsg.value = e.message || 'Invalid credentials'
  } finally {
    loading.value = false
  }
}

function oidcLogin(providerID: string) {
  const redirect = route.query.redirect as string || '/'
  window.location.href = `/auth/oidc/${providerID}/login?redirect=${encodeURIComponent(redirect)}`
}
</script>