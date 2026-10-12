import { describe, it, expect } from 'vitest'
import nacl from 'tweetnacl'
import { decodeBase64, encodeBase64 } from 'tweetnacl-util'
import { generateKeyPair, decryptE2E, channelContext } from '~/utils/crypto'

describe('generateKeyPair', () => {
  it('returns a valid keypair with all expected fields', () => {
    const result = generateKeyPair()

    expect(result).toBeDefined()
    expect(result.publicKey).toBeTruthy()
    expect(result.publicKeyStd).toBeTruthy()
    expect(result.privateKey).toBeTruthy()
    expect(result.keyFile).toBeDefined()
    expect(result.keyFile.public_key).toBeTruthy()
    expect(result.keyFile.private_key).toBeTruthy()
  })

  it('keyFile keys match the std-encoded keys', () => {
    const result = generateKeyPair()

    expect(result.keyFile.public_key).toBe(result.publicKeyStd)
    expect(result.keyFile.private_key).toBe(result.privateKey)
  })

  it('publicKey is RawURL-encoded (no +, /, or =)', () => {
    const result = generateKeyPair()

    expect(result.publicKey).not.toContain('+')
    expect(result.publicKey).not.toContain('/')
    expect(result.publicKey).not.toContain('=')
  })

  it('publicKeyStd and privateKey are valid base64', () => {
    const result = generateKeyPair()

    expect(() => decodeBase64(result.publicKeyStd)).not.toThrow()
    expect(() => decodeBase64(result.privateKey)).not.toThrow()
  })

  it('decoded keys are 32 bytes (NaCl box key size)', () => {
    const result = generateKeyPair()

    const pubDecoded = decodeBase64(result.publicKeyStd)
    const privDecoded = decodeBase64(result.privateKey)

    expect(pubDecoded.length).toBe(32)
    expect(privDecoded.length).toBe(32)
  })

  it('generates unique keys on each call', () => {
    const kp1 = generateKeyPair()
    const kp2 = generateKeyPair()

    expect(kp1.publicKey).not.toBe(kp2.publicKey)
    expect(kp1.privateKey).not.toBe(kp2.privateKey)
    expect(kp1.keyFile.public_key).not.toBe(kp2.keyFile.public_key)
  })

  it('v2Context decrypts a context-bound envelope and rejects a mismatched context', () => {
    const recipient = generateKeyPair()
    const recipientPub = decodeBase64(recipient.publicKeyStd)
    const ephemeral = nacl.box.keyPair()

    const channel = 'chan-a'
    const ctx = channelContext(channel)
    const ctxBytes = new TextEncoder().encode(ctx)
    const plaintext = new TextEncoder().encode('{"hello":"world"}')
    const payload = new Uint8Array(ctxBytes.length + 1 + plaintext.length)
    payload.set(ctxBytes, 0)
    payload[ctxBytes.length] = 0
    payload.set(plaintext, ctxBytes.length + 1)

    const nonce = nacl.randomBytes(24)
    const ciphertext = nacl.box(payload, nonce, recipientPub, ephemeral.secretKey)

    const envelope = {
      encrypted: true,
      version: 2,
      epk: encodeBase64(ephemeral.publicKey),
      nonce: encodeBase64(nonce),
      ciphertext: encodeBase64(ciphertext),
      ctx: encodeBase64(ctxBytes),
    }

    expect(decryptE2E(envelope, recipient.privateKey, ctx)).toBe('{"hello":"world"}')
    expect(() => decryptE2E(envelope, recipient.privateKey, channelContext('chan-b'))).toThrow()
  })

  it('RawURL publicKey can be converted back to std base64', () => {
    const result = generateKeyPair()

    const raw = result.publicKey.replace(/-/g, '+').replace(/_/g, '/')
    const padding = (4 - (raw.length % 4)) % 4
    const std = raw + '='.repeat(padding)

    expect(std).toBe(result.publicKeyStd)
  })
})
