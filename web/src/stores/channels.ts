import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api, type Project } from '../api/client'

export const useChannelsStore = defineStore('channels', () => {
  const channels = ref<Project[]>([])
  const loading = ref(false)

  const channelList = computed(() => channels.value)

  async function fetchChannels() {
    loading.value = true
    try {
      channels.value = await api.listProjects()
    } finally {
      loading.value = false
    }
  }

  async function createChannel(id: string, name?: string) {
    await api.createProject({ id, name: name || id })
    await fetchChannels()
  }

  async function updateChannel(id: string, data: Partial<Project>) {
    await api.updateProject(id, data)
    await fetchChannels()
  }

  async function deleteChannel(id: string) {
    await api.deleteProject(id)
    await fetchChannels()
  }

  return { channels, loading, channelList, fetchChannels, createChannel, updateChannel, deleteChannel }
})