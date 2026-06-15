import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api, type Channel } from '../api/client'

export const useChannelsStore = defineStore('channels', () => {
  const channels = ref<Channel[]>([])
  const loading = ref(false)

  const channelList = computed(() => channels.value)

  async function fetchChannels() {
    loading.value = true
    try {
      channels.value = await api.listChannels()
    } finally {
      loading.value = false
    }
  }

  async function createChannel(id: string, description?: string) {
    await api.createChannel({ id, description })
    await fetchChannels()
  }

  async function updateChannel(id: string, data: Partial<Channel>) {
    await api.updateChannel(id, data)
    await fetchChannels()
  }

  async function deleteChannel(id: string) {
    await api.deleteChannel(id)
    await fetchChannels()
  }

  return { channels, loading, channelList, fetchChannels, createChannel, updateChannel, deleteChannel }
})