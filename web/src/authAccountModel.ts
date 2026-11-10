import type { AuthUser } from './authApi.ts'

export function canDeleteUser(target: AuthUser, currentId: string, users: AuthUser[]) {
  return target.id !== currentId && users.filter(user => user.role === 'admin').length > 1
}
export function keyPayload(data: FormData) {
  const name = String(data.get('name') ?? '').trim()
  const scopes = data.getAll('scopes').map(String)
  if (!name) throw new Error('Enter a key name.')
  if (!scopes.length || scopes.some(scope => !['services:read', 'publications:write'].includes(scope))) throw new Error('Select at least one supported scope.')
  const repositories = [...new Set(String(data.get('repositories') ?? '').split(/[,\n]/).map(value => value.trim()).filter(Boolean))]
  if (scopes.includes('publications:write') && !repositories.length) throw new Error('Publisher keys require at least one exact image repository.')
  if (!scopes.includes('publications:write') && repositories.length) throw new Error('Repository restrictions require the publications:write scope.')
  if (repositories.some(value => value.length > 255 || !/^ghcr\.io\/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:\/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)+$/.test(value))) throw new Error('Use exact GHCR image repositories, such as ghcr.io/acme/site; no tags, digests, URLs or wildcards.')
  const expiry = String(data.get('expiresAt') ?? '')
  if (expiry && (!Number.isFinite(Date.parse(expiry)) || Date.parse(expiry) <= Date.now())) throw new Error('Expiry must be a valid future date.')
  return { name, scopes, repositories, ...(expiry ? { expiresAt: new Date(expiry).toISOString() } : {}) }
}
