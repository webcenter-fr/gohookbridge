import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api, type UserInfo, type AuthMethods } from '../api/client'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<UserInfo | null>(null)
  const authMethods = ref<AuthMethods | null>(null)
  const loaded = ref(false)

  const isAuthenticated = computed(() => user.value !== null)
  const isAdmin = computed(() => user.value?.roles.includes('admin') ?? false)

  async function checkSession() {
    if (loaded.value) return
    try {
      user.value = await api.getMe()
    } catch {
      user.value = null
    }
    loaded.value = true
  }

  async function loadAuthMethods() {
    if (authMethods.value) return
    try {
      authMethods.value = await api.getAuthMethods()
    } catch {
      authMethods.value = null
    }
  }

  async function login(username: string, password: string) {
    await api.login(username, password)
    loaded.value = false
    await checkSession()
  }

  async function logout() {
    await api.logout()
    user.value = null
    loaded.value = false
    authMethods.value = null
  }

  function reset() {
    user.value = null
    loaded.value = false
    authMethods.value = null
  }

  return { user, authMethods, loaded, isAuthenticated, isAdmin, checkSession, loadAuthMethods, login, logout, reset }
})