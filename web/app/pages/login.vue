<template>
  <div class="flex items-center justify-center min-h-screen">
    <UCard class="w-[400px]">
      <template #header>
        <div class="text-center">
          <img src="/logo.svg" alt="gohookbridge" class="h-9 mb-2 mx-auto" />
          <p class="text-sm text-(--ui-text-muted)">Sign in to continue</p>
        </div>
      </template>

      <UAlert v-if="errorMsg" color="error" :close="true" class="mb-4" @update:open="errorMsg = ''">
        <template #description>{{ errorMsg }}</template>
      </UAlert>

      <template v-if="oidcProviders.length > 0">
        <div class="flex flex-col gap-2">
          <UButton
            v-for="p in oidcProviders"
            :key="p.id"
            block
            variant="soft"
            color="neutral"
            @click="oidcLogin(p.id)"
          >
            Login with {{ p.name }}
          </UButton>
        </div>
        <USeparator v-if="localEnabled" label="or" class="my-4" />
      </template>

      <form v-if="localEnabled" class="flex flex-col gap-4" @submit.prevent="handleLogin">
        <UFormField label="Username">
          <UInput v-model="username" autocomplete="username" class="w-full" />
        </UFormField>
        <UFormField label="Password">
          <UInput v-model="password" type="password" autocomplete="current-password" class="w-full" />
        </UFormField>
        <UButton type="submit" block :loading="loading">Sign In</UButton>
      </form>
    </UCard>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useAuthStore } from '~/stores/auth'
import { api } from '~/utils/api'

definePageMeta({ layout: false, public: true, guest: true })

const auth = useAuthStore()
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
    await navigateTo(redirect)
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
