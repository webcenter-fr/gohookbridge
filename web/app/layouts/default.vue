<template>
  <div class="flex h-screen flex-col">
    <header class="flex items-center justify-between h-14 px-6 border-b border-(--ui-border)">
      <NuxtLink to="/" class="flex items-center gap-2 no-underline text-inherit">
        <img src="/logo.svg" alt="gohookbridge" class="h-7 w-[210px] shrink-0" />
      </NuxtLink>
      <div class="flex items-center gap-3">
        <UBadge v-if="auth.user" size="sm" color="neutral">{{ auth.user.username }}</UBadge>
        <UButton size="sm" variant="ghost" color="neutral" @click="handleLogout">Logout</UButton>
      </div>
    </header>
    <div class="flex flex-1 min-h-0">
      <aside class="w-55 border-r border-(--ui-border) p-3 overflow-y-auto">
        <UNavigationMenu v-model="activeKey" orientation="vertical" :items="menuItems" />
      </aside>
      <main class="flex-1 p-6 overflow-y-auto">
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAuthStore } from '~/stores/auth'

const auth = useAuthStore()
const route = useRoute()

const activeKey = ref('dashboard')

const menuItems = computed(() => {
  const items: any[] = [
    { label: 'Dashboard', icon: 'i-lucide-layout-dashboard', value: 'dashboard', to: '/' },
    { label: 'Channels', icon: 'i-lucide-radio-tower', value: 'channels', to: '/channels' },
  ]

  if (auth.isAdmin) {
    items.push(
      { type: 'separator' },
      { label: 'Global Config', icon: 'i-lucide-settings', value: 'admin-global', to: '/admin/global' },
      { label: 'Users', icon: 'i-lucide-users', value: 'admin-users', to: '/admin/users' },
      { label: 'RBAC', icon: 'i-lucide-shield-check', value: 'admin-rbac', to: '/admin/rbac' },
      { label: 'OIDC', icon: 'i-lucide-link', value: 'admin-oidc', to: '/admin/oidc' },
      { label: 'Banned IPs', icon: 'i-lucide-ban', value: 'admin-bans', to: '/admin/bans' },
    )
  }
  return items
})

function syncActiveKey() {
  const path = route.path
  if (path.startsWith('/channels')) {
    activeKey.value = 'channels'
  } else if (path.startsWith('/admin/global')) {
    activeKey.value = 'admin-global'
  } else if (path.startsWith('/admin/users')) {
    activeKey.value = 'admin-users'
  } else if (path.startsWith('/admin/rbac')) {
    activeKey.value = 'admin-rbac'
  } else if (path.startsWith('/admin/oidc')) {
    activeKey.value = 'admin-oidc'
  } else if (path.startsWith('/admin/bans')) {
    activeKey.value = 'admin-bans'
  } else {
    activeKey.value = 'dashboard'
  }
}

watch(() => route.path, syncActiveKey, { immediate: true })

async function handleLogout() {
  await auth.logout()
  await navigateTo('/login')
}
</script>
