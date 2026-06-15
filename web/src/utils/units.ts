export type BodySizeUnit = 'bytes' | 'kib' | 'mib' | 'gib'

export function bodySizeToBytes(val: number, unit: BodySizeUnit): number {
  switch (unit) {
    case 'kib': return val * 1024
    case 'mib': return val * 1024 * 1024
    case 'gib': return val * 1024 * 1024 * 1024
    default: return val
  }
}

export function bytesToBodySizeUnit(bytes: number): { value: number; unit: BodySizeUnit } {
  if (bytes >= 1024 * 1024 * 1024) return { value: bytes / (1024 * 1024 * 1024), unit: 'gib' }
  if (bytes >= 1024 * 1024) return { value: bytes / (1024 * 1024), unit: 'mib' }
  if (bytes >= 1024) return { value: bytes / 1024, unit: 'kib' }
  return { value: bytes, unit: 'bytes' }
}

export const bodySizeUnitOptions = [
  { label: 'B', value: 'bytes' as const },
  { label: 'KiB', value: 'kib' as const },
  { label: 'MiB', value: 'mib' as const },
  { label: 'GiB', value: 'gib' as const },
]