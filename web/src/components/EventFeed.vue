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
    </n-spin>
    <n-list v-if="events.length > 0" style="max-height: 500px; overflow-y: auto;">
      <n-list-item v-for="evt in events" :key="evt.id">
        <template #header>
          <n-space justify="space-between">
            <n-text depth="3" style="font-size: 12px;">#{{ evt.id }}</n-text>
            <n-text depth="3" style="font-size: 12px;">{{ evt.timestamp }}</n-text>
          </n-space>
        </template>
        <json-viewer :data="evt.data" />
      </n-list-item>
    </n-list>
    <n-empty v-else description="No events yet" />
  </n-space>
</template>

<script setup lang="ts">
import { NList, NListItem, NSpace, NTag, NInput, NInputGroup, NInputGroupLabel, NSpin, NEmpty, NH3, NText } from 'naive-ui'
import JsonViewer from './JsonViewer.vue'

defineProps<{
  channel: string
  events: { id: number; data: any; timestamp: string }[]
  connected: boolean
  connecting: boolean
}>()
</script>