<template>
  <div class="flex flex-col gap-4">
    <h3 class="text-lg font-semibold m-0">Banned IPs</h3>
    <div :class="{ 'opacity-50 pointer-events-none': loading }">
      <UTable v-if="!loading" :data="bans" :columns="columns">
        <template #until-cell="{ row }">
          {{ new Date(row.original.until).toLocaleString() }}
        </template>
        <template #actions-cell="{ row }">
          <UButton size="sm" color="warning" variant="soft" @click="handleUnban(row.original.ip)">Unban</UButton>
        </template>
      </UTable>
      <div v-if="!loading && bans.length === 0" class="flex flex-col items-center gap-2 py-8 text-(--ui-text-muted)">
        <UIcon name="i-lucide-inbox" class="size-8" />
        <span>No banned IPs</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { api, type BanEntry } from '~/utils/api'

definePageMeta({ middleware: 'admin' })

const toast = useToast()
const loading = ref(true)
const bans = ref<BanEntry[]>([])
let timer: ReturnType<typeof setInterval> | null = null

const columns: TableColumn<BanEntry>[] = [
  { accessorKey: 'ip', header: 'IP Address' },
  { id: 'until', header: 'Until' },
  { accessorKey: 'unique_failures', header: 'Unique Failures' },
  { id: 'actions', header: 'Actions' },
]

async function fetchBans() {
  try {
    bans.value = await api.listBans()
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to load bans', color: 'error' })
  } finally {
    loading.value = false
  }
}

async function handleUnban(ip: string) {
  try {
    await api.unbanIP(ip)
    toast.add({ title: `Unbanned ${ip}`, color: 'success' })
    await fetchBans()
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to unban', color: 'error' })
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
