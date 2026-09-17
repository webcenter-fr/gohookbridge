export function channelIdError(id: string): string {
  if (!id) return 'Channel ID required'
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]*$/.test(id)) return 'Letters, numbers, hyphens, underscores only'
  if (id.length > 64) return 'Max 64 characters'
  return ''
}

export function channelDescriptionError(description: string): string {
  return description.length > 500 ? 'Max 500 characters' : ''
}
