<template>
  <n-space vertical>
    <n-space justify="space-between" align="center">
      <n-h3 style="margin: 0;">Channels</n-h3>
      <n-space>
        <n-button v-if="checkedRowKeys.length > 0" type="error" @click="showDeleteModal = true">Delete Selected ({{ checkedRowKeys.length }})</n-button>
        <n-button type="primary" @click="showCreate = true">New Channel</n-button>
      </n-space>
    </n-space>

    <n-input
      v-model:value="searchQuery"
      placeholder="Search channels..."
      clearable
    />

    <n-data-table
      :columns="columns"
      :data="filteredChannels"
      :loading="channelsStore.loading"
      :bordered="false"
      :single-line="false"
      :virtual-scroll="filteredChannels.length > 50"
      :max-height="600"
      :row-key="(row: Channel) => row.id"
      v-model:checked-row-keys="checkedRowKeys"
    />

    <n-modal v-model:show="showCreate" title="New Channel" preset="card" style="width: 400px;">
      <n-form ref="formRef" :model="formData" :rules="rules" @submit.prevent="handleCreate">
        <n-form-item label="Channel ID" path="id">
          <n-input v-model:value="formData.id" placeholder="my-webhook-channel" :maxlength="64" />
        </n-form-item>
        <n-form-item label="Description" path="description">
          <n-input v-model:value="formData.description" placeholder="Optional" type="textarea" :maxlength="500" />
        </n-form-item>
        <n-space justify="end">
          <n-button @click="showCreate = false">Cancel</n-button>
          <n-button type="primary" attr-type="submit" :loading="creating">Create</n-button>
        </n-space>
      </n-form>
    </n-modal>

    <n-modal v-model:show="showDeleteModal" title="Delete Channels" preset="card" style="width: 450px;">
      <template v-if="checkedRowKeys.length > 0">
        <n-text type="warning">You are about to delete the following channels. This action cannot be undone.</n-text>
        <n-list>
          <n-list-item v-for="id in checkedRowKeys" :key="id">{{ id }}</n-list-item>
        </n-list>
        <n-space justify="end" style="margin-top: 16px;">
          <n-button @click="showDeleteModal = false">Cancel</n-button>
          <n-button type="error" :loading="deleting" @click="handleDeleteSelected">Delete</n-button>
        </n-space>
      </template>
    </n-modal>
  </n-space>
</template>

<script setup lang="ts">
import { ref, computed, h, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { NSpace, NH3, NButton, NDataTable, NModal, NForm, NFormItem, NInput, NTag, NList, NListItem, NText, type FormInst } from 'naive-ui'
import { useChannelsStore } from '../stores/channels'
import type { Channel } from '../api/client'
import { api } from '../api/client'
import { useMessage } from 'naive-ui'

const channelsStore = useChannelsStore()
const router = useRouter()
const message = useMessage()

const showCreate = ref(false)
const creating = ref(false)
const formRef = ref<FormInst | null>(null)
const formData = reactive({ id: '', description: '' })

const searchQuery = ref('')
const checkedRowKeys = ref<string[]>([])
const showDeleteModal = ref(false)
const deleting = ref(false)

const filteredChannels = computed(() => {
  const q = searchQuery.value.toLowerCase()
  if (!q) return channelsStore.channels
  return channelsStore.channels.filter(ch =>
    ch.id.toLowerCase().includes(q)
  )
})

const rules = {
  id: [
    { required: true, message: 'Channel ID required', trigger: 'blur' },
    { pattern: /^[a-zA-Z0-9][a-zA-Z0-9_-]*$/, message: 'Letters, numbers, hyphens, underscores only', trigger: 'blur' },
    { max: 64, message: 'Max 64 characters', trigger: 'blur' },
  ],
  description: [
    { max: 500, message: 'Max 500 characters', trigger: 'blur' },
  ],
}

const columns = [
  { type: 'selection' as const },
  { title: 'ID', key: 'id' as const },
  {
    title: 'Webhook Secret',
    key: 'webhook_secret' as const,
    render: (row: Channel) => {
      return row.webhook_secret ? h(NTag, { type: 'success', size: 'small' }, () => 'Set') : h(NTag, { type: 'default', size: 'small' }, () => 'Not set')
    },
  },
  {
    title: 'Encryption',
    key: 'encryption_mode' as const,
    render: (row: Channel) => {
      if (row.encryption_mode === 'server_side') {
        return h(NTag, { type: 'info', size: 'small' }, () => 'Server-side')
      }
      if (row.encryption_mode === 'e2e') {
        return h(NTag, { type: 'warning', size: 'small' }, () => 'E2E')
      }
      return h(NTag, { type: 'default', size: 'small' }, () => 'None')
    },
  },
  {
    title: 'Actions',
    key: 'id' as const,
    render: (row: Channel) =>
      h(NSpace, {}, () => [
        h(NButton, { size: 'small', onClick: () => router.push({ name: 'channel-detail', params: { id: row.id } }) }, () => 'View'),
      ]),
  },
]

onMounted(() => {
  channelsStore.fetchChannels()
})

async function handleCreate(e: Event) {
  e.preventDefault()
  try {
    await formRef.value?.validate()
  } catch { return }
  creating.value = true
  try {
    await channelsStore.createChannel(formData.id, formData.description || undefined)
    message.success('Channel created')
    showCreate.value = false
    formData.id = ''
    formData.description = ''
  } catch (e: any) {
    message.error(e.message)
  } finally {
    creating.value = false
  }
}

async function handleDeleteSelected() {
  deleting.value = true
  const ids = [...checkedRowKeys.value]
  const results = await Promise.allSettled(ids.map(id => api.deleteChannel(id)))
  await channelsStore.fetchChannels()
  deleting.value = false
  showDeleteModal.value = false
  const failed: string[] = []
  const ok = results.filter((r, i) => {
    if (r.status === 'fulfilled') return true
    failed.push(ids[i])
    return false
  }).length
  if (failed.length === 0) {
    checkedRowKeys.value = []
    message.success(`Deleted ${ok} channel(s)`)
  } else {
    checkedRowKeys.value = failed
    message.warning(`Deleted ${ok}, failed ${failed.length} channel(s)`)
  }
}
</script>