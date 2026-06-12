<template>
  <n-space vertical>
    <n-space justify="space-between" align="center">
      <n-h3 style="margin: 0;">Channels</n-h3>
      <n-button type="primary" @click="showCreate = true">New Channel</n-button>
    </n-space>

    <n-data-table
      :columns="columns"
      :data="channelsStore.channels"
      :loading="channelsStore.loading"
      :bordered="false"
      :single-line="false"
    />

    <n-modal v-model:show="showCreate" title="New Channel" preset="card" style="width: 400px;">
      <n-form @submit.prevent="handleCreate">
        <n-form-item label="Channel ID">
          <n-input v-model:value="newChannelId" placeholder="my-webhook-channel" />
        </n-form-item>
        <n-form-item label="Name">
          <n-input v-model:value="newChannelName" placeholder="My Channel" />
        </n-form-item>
        <n-space justify="end">
          <n-button @click="showCreate = false">Cancel</n-button>
          <n-button type="primary" attr-type="submit">Create</n-button>
        </n-space>
      </n-form>
    </n-modal>
  </n-space>
</template>

<script setup lang="ts">
import { ref, h, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { NSpace, NH3, NButton, NDataTable, NModal, NForm, NFormItem, NInput, NTag, NText } from 'naive-ui'
import { useChannelsStore } from '../stores/channels'
import type { Project } from '../api/client'

const channelsStore = useChannelsStore()
const router = useRouter()

const showCreate = ref(false)
const newChannelId = ref('')
const newChannelName = ref('')

const columns = [
  { title: 'ID', key: 'id' as const },
  { title: 'Name', key: 'name' as const },
  {
    title: 'Signatures',
    key: 'webhook_signatures' as const,
    render: (row: Project) => {
      const sigs = row.webhook_signatures || []
      return sigs.length === 0 ? '-' : sigs.join(', ')
    },
  },
  {
    title: 'Actions',
    key: 'id' as const,
    render: (row: Project) =>
      h(NSpace, {}, () => [
        h(NButton, { size: 'small', onClick: () => router.push(`/${row.id}`) }, () => 'View'),
      ]),
  },
]

onMounted(() => {
  channelsStore.fetchChannels()
})

async function handleCreate() {
  if (!newChannelId.value) return
  await channelsStore.createChannel(newChannelId.value, newChannelName.value)
  showCreate.value = false
  newChannelId.value = ''
  newChannelName.value = ''
}
</script>