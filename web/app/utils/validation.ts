export function channelIdError(id: string): string {
  if (!id) return 'Channel ID required'
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]*$/.test(id)) return 'Letters, numbers, hyphens, underscores only'
  if (id.length > 64) return 'Max 64 characters'
  return ''
}

export function channelDescriptionError(description: string): string {
  return description.length > 500 ? 'Max 500 characters' : ''
}

export interface OIDCProviderForm {
  id: string
  name: string
  client_id: string
  client_secret: string
  issuer_url: string
}

export interface OIDCProviderErrors {
  id: string
  name: string
  client_id: string
  client_secret: string
  issuer_url: string
}

// oidcProviderErrors validates the OIDC provider form. client_secret is only
// required when creating (isEdit === false); on edit an empty secret keeps the
// stored one. issuer_url must be https, with a loopback http exception to
// mirror the backend.
export function oidcProviderErrors(form: OIDCProviderForm, isEdit: boolean): OIDCProviderErrors {
  const errors: OIDCProviderErrors = {
    id: '',
    name: '',
    client_id: '',
    client_secret: '',
    issuer_url: '',
  }
  if (!form.id.trim()) errors.id = 'ID required'
  if (!form.name.trim()) errors.name = 'Name required'
  if (!form.client_id.trim()) errors.client_id = 'Client ID required'
  if (!isEdit && !form.client_secret.trim()) errors.client_secret = 'Client Secret required'

  const issuer = form.issuer_url.trim()
  if (!issuer) {
    errors.issuer_url = 'Issuer URL required'
  } else if (!issuer.startsWith('https://') && !isLoopbackIssuer(issuer)) {
    errors.issuer_url = 'Issuer URL must start with https:// (http://localhost allowed for dev)'
  }
  return errors
}

function isLoopbackIssuer(issuer: string): boolean {
  return issuer.startsWith('http://localhost') || issuer.startsWith('http://127.0.0.1')
}

export function hasErrors(errors: OIDCProviderErrors): boolean {
  return Object.values(errors).some(e => e !== '')
}
