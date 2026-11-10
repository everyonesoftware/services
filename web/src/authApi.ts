export type AuthUser = { id: string; username: string; role: 'admin' }
export type ApiKey = { id: string; name: string; prefix: string; scopes: string[]; repositories: string[]; createdAt: string; expiresAt?: string; lastUsedAt?: string }
export const sessionExpiredEvent = 'services:session-expired'
export class AuthError extends Error {
  readonly status: number
  constructor(message: string, status: number) { super(message); this.status = status }
}
export function sessionFetch(fetcher: typeof fetch, onExpired: () => void, origin: string): typeof fetch {
  return async (input, init) => {
    const response = await fetcher(input, init)
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, origin)
    if (url.origin === origin && url.pathname.startsWith('/api/') && response.status === 401) onExpired()
    return response
  }
}
type AuthOptions = Omit<RequestInit, 'body'> & { body?: unknown }
export async function authRequest<T = void>(path: string, options: AuthOptions = {}, fetcher: typeof fetch = fetch): Promise<T> {
  const headers = { 'Content-Type': 'application/json', ...options.headers }
  const response = await fetcher(`/api/auth${path}`, { ...options, headers, credentials: 'same-origin', cache: 'no-store', body: options.body === undefined ? (options.method && options.method !== 'GET' ? '{}' : undefined) : JSON.stringify(options.body) })
  if (!response.ok) {
    const text = (await response.text()).trim()
    let message = text
    try { const data = JSON.parse(text); message = data.error || data.message || text } catch { /* Plain text errors are also supported. */ }
    throw new AuthError(message || `Request failed (${response.status})`, response.status)
  }
  return response.status === 204 ? undefined as T : await response.json() as T
}
