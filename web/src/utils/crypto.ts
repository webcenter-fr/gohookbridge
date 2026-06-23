import nacl from 'tweetnacl'
import { decodeBase64, encodeBase64, encodeUTF8 } from 'tweetnacl-util'

export interface EncryptedEnvelope {
  encrypted: boolean
  version: number
  epk: string
  nonce: string
  ciphertext: string
}

export function isE2EEncrypted(data: unknown): boolean {
  if (typeof data !== 'object' || data === null) return false
  const d = data as Record<string, unknown>
  return d.encrypted === true && typeof d.epk === 'string' && typeof d.nonce === 'string' && typeof d.ciphertext === 'string'
}

export function decryptE2E(data: string | Record<string, unknown>, privateKeyBase64: string): string {
  let envelope: EncryptedEnvelope
  if (typeof data === 'string') {
    envelope = JSON.parse(data)
  } else {
    envelope = data as unknown as EncryptedEnvelope
  }
  if (!envelope.encrypted) {
    throw new Error('Payload is not encrypted')
  }

  const ciphertext = decodeBase64(envelope.ciphertext)
  const nonce = decodeBase64(envelope.nonce)
  const ephemeralPubKey = decodeBase64(envelope.epk)
  const privateKey = decodeBase64(privateKeyBase64)

  const fixedNonce = new Uint8Array(24)
  fixedNonce.set(nonce.slice(0, 24))

  const fixedEphemeral = new Uint8Array(32)
  fixedEphemeral.set(ephemeralPubKey.slice(0, 32))

  const fixedPrivate = new Uint8Array(32)
  fixedPrivate.set(privateKey.slice(0, 32))

  const decrypted = nacl.box.open(ciphertext, fixedNonce, fixedEphemeral, fixedPrivate)
  if (!decrypted) {
    throw new Error('Decryption failed')
  }

  return encodeUTF8(decrypted)
}

export interface KeyPairResult {
  publicKey: string
  publicKeyStd: string
  privateKey: string
  keyFile: {
    public_key: string
    private_key: string
  }
}

export function generateKeyPair(): KeyPairResult {
  const kp = nacl.box.keyPair()

  const publicKeyStd = encodeBase64(kp.publicKey)
  const privateKeyStd = encodeBase64(kp.secretKey)

  const publicKeyWire = publicKeyStd.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')

  return {
    publicKey: publicKeyWire,
    publicKeyStd,
    privateKey: privateKeyStd,
    keyFile: {
      public_key: publicKeyStd,
      private_key: privateKeyStd,
    },
  }
}
