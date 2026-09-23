export function safeRedirectPath(redirect: string): string {
  if (!redirect) return '/'
  if (
    redirect.startsWith('/') &&
    !redirect.startsWith('//') &&
    !redirect.startsWith('/\\') &&
    !redirect.includes('\r') &&
    !redirect.includes('\n')
  ) {
    return redirect
  }
  return '/'
}
