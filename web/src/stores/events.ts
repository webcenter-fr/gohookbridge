import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'
import { isE2EEncrypted, decryptE2E } from '../utils/crypto'

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
  let decryptionKey: string | undefined

  function isAESEncrypted(data: unknown): boolean {
    if (typeof data === 'object' && data !== null) {
      const d = data as Record<string, unknown>
      return d.encrypted === true && d.algorithm === 'AES-256-GCM'
    }
    return false
  }

  function connect(channel: string, token?: string, privateKey?: string) {
    disconnect()
    connecting.value = true
    events.value = []
    decryptionKey = privateKey

    let url = `/events/${channel}`
    if (token) {
      url += `?token=${encodeURIComponent(token)}`
    }
    const es = new EventSource(url)

    es.onopen = () => {
      connected.value = true
      connecting.value = false
    }

    es.onmessage = (msg) => {
      let parsed: any
      try {
        parsed = JSON.parse(msg.data)
      } catch {
        events.value.push({
          id: eventCounter++,
          data: msg.data,
          timestamp: new Date().toISOString(),
          raw: msg.data,
          encrypted: false,
        })
        return
      }

      if (parsed.message === 'connected' || parsed.message === 'ready') return

      // For E2E channels, the encrypted envelope (epk/nonce/ciphertext) is
      // nested inside bodyB. The server relays it as-is and never sees the keys.
      // We must decode bodyB to find the actual encrypted payload.
      if (parsed.bodyB && decryptE2E) {
        try {
          const decoded = atob(parsed.bodyB)
          const inner = JSON.parse(decoded)
          if (isE2EEncrypted(inner) && decryptionKey) {
            try {
              const decrypted = decryptE2E(inner, decryptionKey)
              events.value.push({
                id: eventCounter++,
                data: JSON.parse(decrypted),
                timestamp: new Date().toISOString(),
                raw: decrypted,
                encrypted: false,
              })
              return
            } catch {
              events.value.push({
                id: eventCounter++,
                data: inner,
                timestamp: new Date().toISOString(),
                raw: msg.data,
                encrypted: true,
              })
              return
            }
          }
        } catch {
          // bodyB decode failed — fall through to normal handling
        }
      }

      events.value.push({
        id: eventCounter++,
        data: parsed,
        timestamp: new Date().toISOString(),
        raw: msg.data,
        encrypted: isAESEncrypted(parsed) || isE2EEncrypted(parsed),
      })
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
    decryptionKey = undefined
  }

  function clear() {
    events.value = []
    eventCounter = 0
  }

  function setDecryptionKey(key: string | undefined) {
    decryptionKey = key
  }

  return { events, connected, connecting, connect, disconnect, clear, setDecryptionKey }
})