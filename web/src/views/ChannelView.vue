<template>
  <n-spin :show="loading">
    <n-tabs type="line" :value="tab" @update:value="t => tab = t">
      <n-tab-pane name="settings" tab="Settings">
        <n-form v-if="project" label-placement="top" style="max-width: 600px;">
          <n-form-item label="Channel ID">
            <n-input :value="project.id" disabled />
          </n-form-item>
          <n-form-item label="Name">
            <n-input v-model:value="form.name" />
          </n-form-item>
          <n-form-item label="Webhook Signatures">
            <n-dynamic-input v-model:value="form.webhook_signatures" placeholder="secret" />
          </n-form-item>
          <n-form-item label="Allowed IPs">
            <n-dynamic-input v-model:value="form.allowed_ips" placeholder="10.0.0.0/8" />
          </n-form-item>
          <n-form-item label="Max Body Size">
            <n-input-number v-model:value="form.max_body_size" :min="0" />
          </n-form-item>
          <n-form-item label="Replay Token">
            <n-input v-model:value="form.replay_token" placeholder="replay-token" />
          </n-form-item>
          <n-space>
            <n-button type="primary" @click="handleSave">Save</n-button>
            <n-button @click="handleReplay" secondary>Replay Events</n-button>
          </n-space>
        </n-form>
      </n-tab-pane>
      <n-tab-pane name="events" tab="Events">
        <n-space vertical>
          <event-feed
            :channel="channel"
            :events="eventsStore.events"
            :connected="eventsStore.connected"
            :connecting="eventsStore.connecting"
          />
          <n-space>
            <n-button @click="eventsStore.connect(channel)" :disabled="eventsStore.connected">Connect</n-button>
            <n-button @click="eventsStore.disconnect()" :disabled="!eventsStore.connected">Disconnect</n-button>
            <n-button @click="eventsStore.clear()" quaternary>Clear</n-button>
          </n-space>
        </n-space>
      </n-tab-pane>
    </n-tabs>
  </n-spin>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { NSpace, NSpin, NTabs, NTabPane, NForm, NFormItem, NInput, NInputNumber, NButton, NDynamicInput } from 'naive-ui'
import { api } from '../api/client'
import type { Project } from '../api/client'
import { useEventsStore } from '../stores/events'
import EventFeed from '../components/EventFeed.vue'
import { useMessage } from 'naive-ui'

const route = useRoute()
const channel = route.params.channel as string
const eventsStore = useEventsStore()
const message = useMessage()

const loading = ref(true)
const project = ref<Project | null>(null)
const tab = ref('settings')

const form = reactive({
  name: '',
  webhook_signatures: [''] as string[],
  allowed_ips: [''] as string[],
  max_body_size: 26214400,
  replay_token: '',
})

onMounted(async () => {
  try {
    project.value = await api.getProject(channel)
    form.name = project.value.name || ''
    form.webhook_signatures = (project.value.webhook_signatures?.length ? project.value.webhook_signatures : [''])
    form.allowed_ips = (project.value.allowed_ips?.length ? project.value.allowed_ips : [''])
    form.max_body_size = project.value.max_body_size || 26214400
    form.replay_token = project.value.replay_token || ''
  } catch (e: any) {
    message.error(e.message || 'Failed to load channel')
  } finally {
    loading.value = false
  }
})

onUnmounted(() => {
  eventsStore.disconnect()
})

async function handleSave() {
  const clean = (arr: string[]) => arr.filter(s => s.trim() !== '')
  try {
    await api.updateProject(channel, {
      name: form.name,
      webhook_signatures: clean(form.webhook_signatures),
      allowed_ips: clean(form.allowed_ips),
      max_body_size: form.max_body_size,
      replay_token: form.replay_token,
    })
    message.success('Saved')
  } catch (e: any) {
    message.error(e.message || 'Failed to save')
  }
}

async function handleReplay() {
  try {
    await fetch(`/replay/${channel}`, { method: 'POST', credentials: 'same-origin' })
    message.success('Replay triggered')
  } catch (e: any) {
    message.error(e.message || 'Replay failed')
  }
}
</script>