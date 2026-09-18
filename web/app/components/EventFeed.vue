<template>
  <div class="flex flex-col gap-3">
    <div class="flex items-center justify-between">
      <h3 class="text-lg font-semibold m-0">Events</h3>
      <div class="flex items-center gap-2">
        <UBadge v-if="connected" color="success" size="sm">Connected</UBadge>
        <UBadge v-else-if="connecting" color="warning" size="sm">Connecting...</UBadge>
        <UBadge v-else color="error" size="sm">Disconnected</UBadge>
      </div>
    </div>

    <div :class="{ 'opacity-50 pointer-events-none': connecting }">
      <div class="flex items-center gap-2">
        <span class="text-sm text-(--ui-text-muted)">Channel</span>
        <UInput :model-value="channel" readonly class="flex-1" />
      </div>
      <p v-if="messageTTL" class="text-xs text-(--ui-text-muted) mt-1">
        Messages retained for {{ formatTTL(messageTTL) }}
      </p>
    </div>

    <ul v-if="events.length > 0" class="divide-y divide-(--ui-border) max-h-[500px] overflow-y-auto">
      <li v-for="evt in events" :key="evt.id" class="py-3">
        <div class="flex items-center justify-between mb-1">
          <div class="flex items-center gap-2">
            <span class="text-xs text-(--ui-text-muted)">#{{ evt.id }}</span>
            <span v-if="evt.event_id" class="text-xs text-(--ui-text-muted)">{{ evt.event_id.slice(0, 8) }}...</span>
            <UBadge v-if="evt.encrypted && encryptionMode === 'e2e'" color="warning" size="sm">E2E Encrypted</UBadge>
            <UBadge v-else-if="evt.encrypted" color="warning" size="sm">Encrypted</UBadge>
          </div>
          <div class="flex items-center gap-2">
            <span class="text-xs text-(--ui-text-muted)">{{ evt.timestamp }}</span>
            <UButton size="xs" variant="ghost" color="neutral" @click="emit('replay', evt.event_id || String(evt.id))">Replay</UButton>
          </div>
        </div>
        <UAlert v-if="evt.encrypted && encryptionMode === 'e2e'" color="warning" title="E2E Encrypted" class="mb-1">
          <template #description>
            This channel uses end-to-end encryption. Provide the private key in the channel Data tab to decrypt in the browser, or use the CLI client with <code>--encryption-key-file</code>.
          </template>
        </UAlert>
        <UAlert v-else-if="evt.encrypted" color="warning" title="Encrypted" class="mb-1">
          <template #description>
            This channel uses server-side encryption. Events are encrypted. Use the CLI client with <code>--encryption-key</code> to decrypt.
          </template>
        </UAlert>
        <JsonViewer :data="evt.data" />
      </li>
    </ul>
    <div v-else class="flex flex-col items-center gap-2 py-8 text-(--ui-text-muted)">
      <UIcon name="i-lucide-inbox" class="size-8" />
      <span>No events yet</span>
    </div>
  </div>
</template>

<script setup lang="ts">
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
