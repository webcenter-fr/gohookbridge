<template>
  <n-layout position="absolute" style="height: 100vh">
    <n-layout-header bordered style="padding: 0 24px; display: flex; align-items: center; justify-content: space-between; height: 56px;">
      <div style="display: flex; align-items: center; gap: 16px;">
        <router-link to="/" style="text-decoration: none; color: inherit;">
          <n-h4 style="margin: 0;">
            <n-text type="info">gohookbridge</n-text>
          </n-h4>
        </router-link>
      </div>
      <div style="display: flex; align-items: center; gap: 12px;">
        <n-tag v-if="auth.user" size="small">{{ auth.user.username }}</n-tag>
        <n-button size="small" quaternary @click="handleLogout">Logout</n-button>
      </div>
    </n-layout-header>
    <n-layout has-sider position="absolute" style="top: 56px">
      <n-layout-sider bordered width="220" content-style="padding: 12px;">
        <n-menu :value="activeKey" :options="menuOptions" @update:value="handleMenuSelect" />
      </n-layout-sider>
      <n-layout content-style="padding: 24px; overflow-y: auto;">
        <router-view />
      </n-layout>
    </n-layout>
  </n-layout>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { NLayout, NLayoutHeader, NLayoutSider, NButton, NMenu, NTag, NH4, NText } from 'naive-ui'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const activeKey = computed(() => {
  const name = route.name as string
  if (name === 'channel-detail') return 'channels'
  return name || 'dashboard'
})

const menuOptions = computed(() => {
  const items: any[] = [
    { key: 'dashboard', label: 'Dashboard', icon: () => h('span', '📋') },
    { key: 'channels', label: 'Channels', icon: () => h('span', '📡') },
  ]

  if (auth.isAdmin) {
    items.push(
      { type: 'divider' as const, key: 'admin-divider' },
      { key: 'admin-global', label: 'Global Config', icon: () => h('span', '⚙️') },
      { key: 'admin-users', label: 'Users', icon: () => h('span', '👥') },
      { key: 'admin-rbac', label: 'RBAC', icon: () => h('span', '🔐') },
      { key: 'admin-oidc', label: 'OIDC', icon: () => h('span', '🔗') },
    )
  }
  return items
})

function handleMenuSelect(key: string) {
  router.push({ name: key })
}

async function handleLogout() {
  await auth.logout()
  router.push('/login')
}
</script>