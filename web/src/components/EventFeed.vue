<template>
  <n-space vertical>
    <n-space justify="space-between" align="center">
      <n-h3 style="margin: 0;">Events</n-h3>
      <n-space>
        <n-tag v-if="connected" type="success" size="small">Connected</n-tag>
        <n-tag v-else-if="connecting" type="warning" size="small">Connecting...</n-tag>
        <n-tag v-else type="error" size="small">Disconnected</n-tag>
      </n-space>
    </n-space>
    <n-spin :show="connecting">
      <n-input-group>
        <n-input-group-label>Channel</n-input-group-label>
        <n-input :value="channel" readonly />
      </n-input-group>
      <n-text v-if="messageTTL" depth="3" style="font-size: 12px; margin-top: 4px; display: block;">
        Messages retained for {{ formatTTL(messageTTL) }}
      </n-text>
    </n-spin>
    <n-list v-if="events.length > 0" style="max-height: 500px; overflow-y: auto;">
      <n-list-item v-for="evt in events" :key="evt.id">
        <n-space justify="space-between" style="margin-bottom: 4px;">
          <n-space>
            <n-text depth="3" style="font-size: 12px;">#{{ evt.id }}</n-text>
            <n-text v-if="evt.event_id" depth="3" style="font-size: 12px;">{{ evt.event_id.slice(0, 8) }}...</n-text>
            <n-tag v-if="evt.encrypted && encryptionMode === 'e2e'" type="warning" size="small">E2E Encrypted</n-tag>
            <n-tag v-else-if="evt.encrypted" type="warning" size="small">Encrypted</n-tag>
          </n-space>
          <n-space>
            <n-text depth="3" style="font-size: 12px;">{{ evt.timestamp }}</n-text>
            <n-button size="tiny" quaternary @click="() => emit('replay', evt.event_id || String(evt.id))">Replay</n-button>
          </n-space>
        </n-space>
        <n-alert v-if="evt.encrypted && encryptionMode === 'e2e'" type="warning" title="E2E Encrypted" :bordered="false" style="margin-bottom: 4px;">
          This channel uses end-to-end encryption. Events are encrypted. Use the CLI client with <n-code>--encryption-key-file</n-code> to decrypt.
        </n-alert>
        <n-alert v-else-if="evt.encrypted" type="warning" title="Encrypted" :bordered="false" style="margin-bottom: 4px;">
          This channel uses server-side encryption. Events are encrypted. Use the CLI client with <n-code>--encryption-key</n-code> to decrypt.
        </n-alert>
        <json-viewer :data="evt.data" />
      </n-list-item>
    </n-list>
    <n-empty v-else description="No events yet" />
  </n-space>
</template>

<script setup lang="ts">
import { NList, NListItem, NSpace, NTag, NInput, NInputGroup, NInputGroupLabel, NSpin, NEmpty, NH3, NText, NButton, NAlert, NCode } from 'naive-ui'
import JsonViewer from './JsonViewer.vue'

defineProps<{
  channel: string
  events: { id: number; data: any; timestamp: string; event_id?: string; encrypted?: boolean }[]
  connected: boolean
  connecting: boolean
  messageTTL?: number
  encryptionMode?: string
}>()

const emit = defineEmits<{
  (e: 'replay', eventId: string): void
}>()

function formatTTL(seconds: number): string {
  if (seconds >= 86400) return `${Math.floor(seconds / 86400)}d`
  if (seconds >= 3600) return `${Math.floor(seconds / 3600)}h`
  if (seconds >= 60) return `${Math.floor(seconds / 60)}m`
  return `${seconds}s`
}
</script>