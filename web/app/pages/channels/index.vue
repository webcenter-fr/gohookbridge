<template>
  <div class="flex flex-col gap-4">
    <div class="flex items-center justify-between">
      <h3 class="text-lg font-semibold m-0">Channels</h3>
      <div class="flex items-center gap-2">
        <UButton v-if="selectedIds.length > 0" color="error" @click="showDeleteModal = true">
          Delete Selected ({{ selectedIds.length }})
        </UButton>
        <UButton color="primary" @click="showCreate = true">New Channel</UButton>
      </div>
    </div>

    <UInput v-model="searchQuery" placeholder="Search channels..." class="w-full" />

    <UTable :data="filteredChannels" :columns="columns" :loading="channelsStore.loading">
      <template #select-header>
        <UCheckbox
          :model-value="allSelected ? true : (someSelected ? 'indeterminate' : false)"
          @update:model-value="toggleAll"
        />
      </template>
      <template #select-cell="{ row }">
        <UCheckbox
          :model-value="selectedIds.includes(row.original.id)"
          @update:model-value="(v: boolean | 'indeterminate') => toggleOne(row.original.id, v === true)"
        />
      </template>
      <template #webhook_secret-cell="{ row }">
        <UBadge v-if="row.original.webhook_secret" color="success" size="sm">Set</UBadge>
        <UBadge v-else color="neutral" size="sm">Not set</UBadge>
      </template>
      <template #encryption_mode-cell="{ row }">
        <UBadge v-if="row.original.encryption_mode === 'server_side'" color="info" size="sm">Server-side</UBadge>
        <UBadge v-else-if="row.original.encryption_mode === 'e2e'" color="warning" size="sm">E2E</UBadge>
        <UBadge v-else color="neutral" size="sm">None</UBadge>
      </template>
      <template #actions-cell="{ row }">
        <UButton size="sm" color="neutral" variant="soft" @click="navigateTo(`/channels/${row.original.id}`)">View</UButton>
      </template>
    </UTable>

    <UModal v-model:open="showCreate" title="New Channel">
      <template #body>
        <div class="flex flex-col gap-4">
          <UFormField label="Channel ID" :error="errors.id">
            <UInput v-model="formData.id" placeholder="my-webhook-channel" :maxlength="64" class="w-full" />
          </UFormField>
          <UFormField label="Description" :error="errors.description">
            <UTextarea v-model="formData.description" placeholder="Optional" :maxlength="500" class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="neutral" variant="ghost" @click="showCreate = false">Cancel</UButton>
          <UButton color="primary" :loading="creating" @click="handleCreate">Create</UButton>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="showDeleteModal" title="Delete Channels">
      <template #body>
        <div v-if="selectedIds.length > 0" class="flex flex-col gap-3">
          <p class="text-(--ui-text-warning)">You are about to delete the following channels. This action cannot be undone.</p>
          <ul class="divide-y divide-(--ui-border)">
            <li v-for="id in selectedIds" :key="id" class="py-1">{{ id }}</li>
          </ul>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="neutral" variant="ghost" @click="showDeleteModal = false">Cancel</UButton>
          <UButton color="error" :loading="deleting" @click="handleDeleteSelected">Delete</UButton>
        </div>
      </template>
    </UModal>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, reactive, onMounted } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { useChannelsStore } from '~/stores/channels'
import { api, type Channel } from '~/utils/api'
import { channelIdError, channelDescriptionError } from '~/utils/validation'

const channelsStore = useChannelsStore()
const toast = useToast()

const showCreate = ref(false)
const creating = ref(false)
const formData = reactive({ id: '', description: '' })
const errors = reactive({ id: '', description: '' })

const searchQuery = ref('')
const selectedIds = ref<string[]>([])
const showDeleteModal = ref(false)
const deleting = ref(false)

const filteredChannels = computed(() => {
  const q = searchQuery.value.toLowerCase()
  if (!q) return channelsStore.channels
  return channelsStore.channels.filter(ch =>
    ch.id.toLowerCase().includes(q)
  )
})

const allSelected = computed(() =>
  filteredChannels.value.length > 0 && filteredChannels.value.every(ch => selectedIds.value.includes(ch.id))
)
const someSelected = computed(() => selectedIds.value.length > 0)

const columns: TableColumn<Channel>[] = [
  { id: 'select', header: '' },
  { accessorKey: 'id', header: 'ID' },
  { id: 'webhook_secret', header: 'Webhook Secret' },
  { id: 'encryption_mode', header: 'Encryption' },
  { id: 'actions', header: 'Actions' },
]

function validate(): boolean {
  errors.id = channelIdError(formData.id)
  errors.description = channelDescriptionError(formData.description)
  return !errors.id && !errors.description
}

function toggleAll(value: boolean | 'indeterminate') {
  selectedIds.value = value === true ? filteredChannels.value.map(ch => ch.id) : []
}

function toggleOne(id: string, checked: boolean) {
  if (checked) {
    if (!selectedIds.value.includes(id)) selectedIds.value = [...selectedIds.value, id]
  } else {
    selectedIds.value = selectedIds.value.filter(x => x !== id)
  }
}

onMounted(() => {
  channelsStore.fetchChannels()
})

async function handleCreate() {
  if (!validate()) return
  creating.value = true
  try {
    await channelsStore.createChannel(formData.id, formData.description || undefined)
    toast.add({ title: 'Channel created', color: 'success' })
    showCreate.value = false
    formData.id = ''
    formData.description = ''
  } catch (e: any) {
    toast.add({ title: e.message, color: 'error' })
  } finally {
    creating.value = false
  }
}

async function handleDeleteSelected() {
  deleting.value = true
  const ids = [...selectedIds.value]
  const results = await Promise.allSettled(ids.map(id => api.deleteChannel(id)))
  await channelsStore.fetchChannels()
  deleting.value = false
  showDeleteModal.value = false
  const failed: string[] = []
  const ok = results.filter((r, i) => {
    if (r.status === 'fulfilled') return true
    const id = ids[i]
    if (id) failed.push(id)
    return false
  }).length
  if (failed.length === 0) {
    selectedIds.value = []
    toast.add({ title: `Deleted ${ok} channel(s)`, color: 'success' })
  } else {
    selectedIds.value = failed
    toast.add({ title: `Deleted ${ok}, failed ${failed.length} channel(s)`, color: 'warning' })
  }
}
</script>
