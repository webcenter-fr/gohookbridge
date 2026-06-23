<template>
  <n-space vertical>
    <n-h3>Banned IPs</n-h3>
    <n-spin :show="loading">
      <n-data-table v-if="!loading" :columns="columns" :data="bans" :bordered="true" />
      <n-empty v-if="!loading && bans.length === 0" description="No banned IPs" />
    </n-spin>
  </n-space>
</template>

<script setup lang="ts">
import { ref, h, onMounted, onUnmounted } from 'vue'
import { NSpace, NH3, NSpin, NDataTable, NEmpty, NButton, useMessage } from 'naive-ui'
import { api, type BanEntry } from '../api/client'

const message = useMessage()
const loading = ref(true)
const bans = ref<BanEntry[]>([])
let timer: ReturnType<typeof setInterval> | null = null

const columns = [
  { title: 'IP Address', key: 'ip' },
  {
    title: 'Until',
    key: 'until',
    render: (row: BanEntry) => new Date(row.until).toLocaleString(),
  },
  { title: 'Unique Failures', key: 'unique_failures' },
  {
    title: 'Actions',
    key: 'actions',
    render: (row: BanEntry) =>
      h(NButton, {
        size: 'small',
        type: 'warning',
        onClick: () => handleUnban(row.ip),
      }, { default: () => 'Unban' }),
  },
]

async function fetchBans() {
  try {
    bans.value = await api.listBans()
  } catch (e: any) {
    message.error(e.message || 'Failed to load bans')
  } finally {
    loading.value = false
  }
}

async function handleUnban(ip: string) {
  try {
    await api.unbanIP(ip)
    message.success(`Unbanned ${ip}`)
    await fetchBans()
  } catch (e: any) {
    message.error(e.message || 'Failed to unban')
  }
}

onMounted(() => {
  fetchBans()
  timer = setInterval(fetchBans, 30000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>