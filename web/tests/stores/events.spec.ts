import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import nacl from 'tweetnacl'
import { encodeBase64 } from 'tweetnacl-util'
import { useEventsStore } from '~/stores/events'

class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  onopen: (() => void) | null = null
  onmessage: ((msg: { data: string }) => void) | null = null
  onerror: (() => void) | null = null
  closed = false

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  close() {
    this.closed = true
  }
}

vi.stubGlobal('EventSource', FakeEventSource)

function lastSource(): FakeEventSource {
  return FakeEventSource.instances[FakeEventSource.instances.length - 1]!
}

describe('useEventsStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    FakeEventSource.instances = []
  })

  it('connect opens an EventSource and marks connected on open', () => {
    const store = useEventsStore()

    store.connect('my-channel')

    expect(store.connecting).toBe(true)
    expect(lastSource().url).toBe('/events/my-channel')

    lastSource().onopen?.()

    expect(store.connected).toBe(true)
    expect(store.connecting).toBe(false)
  })

  it('connect appends the token query parameter', () => {
    const store = useEventsStore()

    store.connect('my-channel', 'tok en')

    expect(lastSource().url).toBe('/events/my-channel?token=tok%20en')
  })

  it('parses JSON messages into events', () => {
    const store = useEventsStore()
    store.connect('my-channel')

    lastSource().onmessage?.({ data: JSON.stringify({ hello: 'world' }) })

    expect(store.events).toHaveLength(1)
    expect(store.events[0]!.data).toEqual({ hello: 'world' })
    expect(store.events[0]!.encrypted).toBe(false)
  })

  it('ignores connected/ready control messages', () => {
    const store = useEventsStore()
    store.connect('my-channel')

    lastSource().onmessage?.({ data: JSON.stringify({ message: 'connected' }) })
    lastSource().onmessage?.({ data: JSON.stringify({ message: 'ready' }) })

    expect(store.events).toHaveLength(0)
  })

  it('keeps non-JSON payloads as raw strings', () => {
    const store = useEventsStore()
    store.connect('my-channel')

    lastSource().onmessage?.({ data: 'not-json' })

    expect(store.events).toHaveLength(1)
    expect(store.events[0]!.data).toBe('not-json')
  })

  it('marks AES-encrypted payloads', () => {
    const store = useEventsStore()
    store.connect('my-channel')

    lastSource().onmessage?.({ data: JSON.stringify({ encrypted: true, algorithm: 'AES-256-GCM' }) })

    expect(store.events[0]!.encrypted).toBe(true)
  })

  it('decrypts E2E bodyB envelopes when a private key is provided', () => {
    const recipient = nacl.box.keyPair()
    const ephemeral = nacl.box.keyPair()
    const nonce = nacl.randomBytes(24)
    const plaintext = new TextEncoder().encode(JSON.stringify({ secret: 'value' }))
    const ciphertext = nacl.box(plaintext, nonce, recipient.publicKey, ephemeral.secretKey)
    const envelope = {
      encrypted: true,
      version: 1,
      epk: encodeBase64(ephemeral.publicKey),
      nonce: encodeBase64(nonce),
      ciphertext: encodeBase64(ciphertext),
    }
    const bodyB = btoa(JSON.stringify(envelope))

    const store = useEventsStore()
    store.connect('my-channel', undefined, encodeBase64(recipient.secretKey))

    lastSource().onmessage?.({ data: JSON.stringify({ bodyB }) })

    expect(store.events).toHaveLength(1)
    expect(store.events[0]!.data).toEqual({ secret: 'value' })
    expect(store.events[0]!.encrypted).toBe(false)
  })

  it('marks E2E bodyB envelopes as encrypted when decryption fails', () => {
    const recipient = nacl.box.keyPair()
    const wrongKey = nacl.box.keyPair()
    const ephemeral = nacl.box.keyPair()
    const nonce = nacl.randomBytes(24)
    const plaintext = new TextEncoder().encode(JSON.stringify({ secret: 'value' }))
    const ciphertext = nacl.box(plaintext, nonce, recipient.publicKey, ephemeral.secretKey)
    const envelope = {
      encrypted: true,
      version: 1,
      epk: encodeBase64(ephemeral.publicKey),
      nonce: encodeBase64(nonce),
      ciphertext: encodeBase64(ciphertext),
    }
    const bodyB = btoa(JSON.stringify(envelope))

    const store = useEventsStore()
    store.connect('my-channel', undefined, encodeBase64(wrongKey.secretKey))

    lastSource().onmessage?.({ data: JSON.stringify({ bodyB }) })

    expect(store.events).toHaveLength(1)
    expect(store.events[0]!.encrypted).toBe(true)
  })

  it('onerror marks disconnected', () => {
    const store = useEventsStore()
    store.connect('my-channel')
    lastSource().onopen?.()

    lastSource().onerror?.()

    expect(store.connected).toBe(false)
    expect(store.connecting).toBe(false)
  })

  it('disconnect closes the source and resets state', () => {
    const store = useEventsStore()
    store.connect('my-channel')
    const source = lastSource()

    store.disconnect()

    expect(source.closed).toBe(true)
    expect(store.connected).toBe(false)
    expect(store.connecting).toBe(false)
  })

  it('clear empties the event list', () => {
    const store = useEventsStore()
    store.connect('my-channel')
    lastSource().onmessage?.({ data: JSON.stringify({ a: 1 }) })

    store.clear()

    expect(store.events).toHaveLength(0)
  })
})
