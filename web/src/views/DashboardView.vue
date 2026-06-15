<template>
  <n-space vertical>
    <n-space justify="space-between" align="center">
      <n-h3 style="margin: 0;">Dashboard</n-h3>
      <n-button type="primary" @click="showCreate = true">New Channel</n-button>
    </n-space>

    <n-card>
      <n-statistic title="Channels" :value="channelsStore.channels.length">
        <template #suffix>
          <n-text depth="3">channels available</n-text>
        </template>
      </n-statistic>
    </n-card>

    <n-h4>Channel Shortcuts</n-h4>
    <n-grid v-if="channelsStore.channels.length > 0" :cols="4" :x-gap="12" :y-gap="12">
      <n-gi v-for="ch in channelsStore.channels" :key="ch.id">
        <n-card
          :title="ch.name || ch.id"
          size="small"
          hoverable
          @click="router.push({ name: 'channel-detail', params: { id: ch.id } })"
          style="cursor: pointer;"
        >
          <n-space vertical>
            <n-text depth="3" style="font-size: 12px;">{{ ch.id }}</n-text>
            <n-tag v-if="ch.encryption_mode && ch.encryption_mode !== 'none'" type="warning" size="tiny">Encrypted</n-tag>
          </n-space>
        </n-card>
      </n-gi>
    </n-grid>
    <n-empty v-else-if="!channelsStore.loading" description="No channels available" />

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
  </n-space>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { NSpace, NH3, NH4, NButton, NModal, NForm, NFormItem, NInput, NCard, NGrid, NGi, NText, NTag, NStatistic, NEmpty, type FormInst } from 'naive-ui'
import { useChannelsStore } from '../stores/channels'
import { useMessage } from 'naive-ui'

const channelsStore = useChannelsStore()
const router = useRouter()
const message = useMessage()

const showCreate = ref(false)
const creating = ref(false)
const formRef = ref<FormInst | null>(null)
const formData = reactive({ id: '', description: '' })

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
</script>