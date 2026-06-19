import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'

export interface SSHEvent {
  id: number
  data: any
  timestamp: string
  raw: string
  event_id?: string
  encrypted: boolean
}

export const useEventsStore = defineStore('events', () => {
  const events = ref<SSHEvent[]>([])
  const connected = ref(false)
  const connecting = ref(false)
  const eventSource = shallowRef<EventSource | null>(null)

  let eventCounter = 0

  function isAESEncrypted(data: unknown): boolean {
    if (typeof data === 'object' && data !== null) {
      const d = data as Record<string, unknown>
      return d.encrypted === true && d.algorithm === 'AES-256-GCM'
    }
    return false
  }

  function connect(channel: string) {
    disconnect()
    connecting.value = true
    events.value = []

    const url = `/events/${channel}`
    const es = new EventSource(url)

    es.onopen = () => {
      connected.value = true
      connecting.value = false
    }

    es.onmessage = (msg) => {
      try {
        const parsed = JSON.parse(msg.data)
        if (parsed.message === 'connected' || parsed.message === 'ready') return
        events.value.push({
          id: eventCounter++,
          data: parsed,
          timestamp: new Date().toISOString(),
          raw: msg.data,
          encrypted: isAESEncrypted(parsed),
        })
      } catch {
        events.value.push({
          id: eventCounter++,
          data: msg.data,
          timestamp: new Date().toISOString(),
          raw: msg.data,
          encrypted: false,
        })
      }
    }

    es.onerror = () => {
      connected.value = false
      connecting.value = false
    }

    eventSource.value = es
  }

  function disconnect() {
    if (eventSource.value) {
      eventSource.value.close()
      eventSource.value = null
    }
    connected.value = false
    connecting.value = false
  }

  function clear() {
    events.value = []
    eventCounter = 0
  }

  return { events, connected, connecting, connect, disconnect, clear }
})