import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { authRequest } from './authApi.ts'
import { AppShell } from './AppShell.tsx'
import { PageHeader } from './PageHeader.tsx'
import { WebAppUpdates } from './WebAppUpdates.tsx'
import type { ApiKey, AuthUser } from './authApi.ts'

import { canDeleteUser, keyPayload } from './authAccountModel.ts'
const errorMessage = (cause: unknown) => cause instanceof Error ? cause.message : 'Request failed. Please try again.'
const dateLabel = (value?: string) => value ? new Date(value).toLocaleString() : 'Never'

type Props = { user: AuthUser; onBack: () => void; onPublishers?: () => void; onLogout: () => void; onPasswordChanged: () => void; sessionError?: string }
export function AuthAccounts({ user, onBack, onPublishers, onLogout, onPasswordChanged, sessionError }: Props) {
  const [users, setUsers] = useState<AuthUser[]>([])
  const [keys, setKeys] = useState<ApiKey[]>([])
  const [loaded, setLoaded] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [token, setToken] = useState('')
  const [publisher, setPublisher] = useState(false)
  const [section, setSection] = useState('Updates')
  const load = useCallback(async () => {
    const [accounts, credentials] = await Promise.all([authRequest<{ users: AuthUser[] }>('/users'), authRequest<{ keys: ApiKey[] }>('/keys')])
    setUsers(accounts.users); setKeys(credentials.keys); setLoaded(true)
  }, [])
  useEffect(() => {
    let active = true
    void Promise.all([authRequest<{ users: AuthUser[] }>('/users'), authRequest<{ keys: ApiKey[] }>('/keys')]).then(([accounts, credentials]) => {
      if (active) { setUsers(accounts.users); setKeys(credentials.keys); setLoaded(true) }
    }).catch(cause => { if (active) setError(errorMessage(cause)) })
    return () => { active = false }
  }, [])
  const perform = async (action: () => Promise<void>) => {
    if (busy) return
    setBusy(true); setError(''); setNotice('')
    try { await action() } catch (cause) { setError(errorMessage(cause)) }
    finally { setBusy(false) }
  }
  const addUser = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    const body = { username: String(data.get('username')), password: String(data.get('password')) }
    void perform(async () => {
      try { await authRequest('/users', { method: 'POST', body }); form.reset(); setNotice('Administrator created.'); await load() }
      finally { form.querySelector<HTMLInputElement>('[name=password]')!.value = '' }
    })
  }
  const changePassword = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const password = String(new FormData(form).get('password'))
    void perform(async () => {
      try { await authRequest('/password', { method: 'PUT', body: { password } }); form.reset(); onPasswordChanged() }
      finally { form.querySelector<HTMLInputElement>('[name=password]')!.value = '' }
    })
  }
  const createKey = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    void perform(async () => {
      const result = await authRequest<{ key: ApiKey; token: string }>('/keys', { method: 'POST', body: keyPayload(new FormData(form)) })
      setToken(result.token); form.reset(); setPublisher(false)
      await load()
    })
  }
  return <AppShell active="settings" user={user} onMonitor={onBack} onPublishers={onPublishers} onLogout={onLogout} sessionError={sessionError} disabled={busy}>
    <main className="workspace accounts-workspace"><PageHeader title="Settings" actions={section !== 'Updates' && <button className="button secondary" disabled={busy} onClick={() => void perform(load)}>Refresh</button>} />
    {error && <p role="alert" className="notice danger">{error}</p>}{notice && <p role="status" className="notice">{notice}</p>}
    <nav className="account-sections" aria-label="Settings sections">{['Updates', 'Administrators', 'API keys', 'Password'].map(name => <button type="button" key={name} aria-pressed={section === name} disabled={busy} onClick={() => setSection(name)}>{name}</button>)}</nav>
    <WebAppUpdates hidden={section !== 'Updates'} disabled={busy} />
    <div className="accounts-grid"><section className="auth-card" aria-labelledby="admins-title" hidden={section !== 'Administrators'}><h2 id="admins-title">Administrators</h2><p className="auth-help">All administrator accounts have full access. Your current account and the last administrator cannot be deleted.</p>
    {!loaded ? <p role="status">{error ? 'Accounts unavailable. Use Refresh to retry.' : 'Loading accounts…'}</p> : <ul className="auth-list">{users.map(account => <li key={account.id}><div><strong>{account.username}</strong><span className="auth-help">{account.role}{account.id === user.id ? ' · You' : ''}</span></div><button className="button secondary" disabled={busy || !canDeleteUser(account, user.id, users)} onClick={() => {
      if (window.confirm(`Delete administrator ${account.username}? This cannot be undone.`)) void perform(async () => { await authRequest(`/users/${encodeURIComponent(account.id)}`, { method: 'DELETE' }); await load(); setNotice('Administrator deleted.') })
    }}>Delete<span className="sr-only"> {account.username}</span></button></li>)}</ul>}
    <form className="auth-form" onSubmit={addUser}><h3>Add administrator</h3><label>Username<input className="icon-url" name="username" autoComplete="off" required /></label><label>Password<input className="icon-url" name="password" type="password" autoComplete="new-password" minLength={15} maxLength={1024} required /></label><p className="auth-help">At least 15 Unicode characters; at most 1024 UTF-8 bytes.</p><button className="button primary" disabled={busy || !loaded}>Add administrator</button></form>
    </section><section className="auth-card" aria-labelledby="password-title" hidden={section !== 'Password'}><h2 id="password-title">Change password</h2><p className="auth-help">Changing your password invalidates existing sessions. You will need to sign in again.</p><form className="auth-form" onSubmit={changePassword}><label>New password<input className="icon-url" name="password" type="password" autoComplete="new-password" minLength={15} maxLength={1024} required /></label><p className="auth-help">At least 15 Unicode characters; at most 1024 UTF-8 bytes.</p><button className="button primary" disabled={busy}>Change password</button></form></section></div>
    <section className="auth-card keys-card" aria-labelledby="keys-title" hidden={section !== 'API keys'}><h2 id="keys-title">API keys</h2><p className="auth-help">Tokens are shown only once. Store them securely; they are never saved in browser storage. API keys do not grant administrator access.</p>
    {token && <section className="key-reveal" aria-label="New API key"><h3>Copy your new API key now</h3><p className="auth-help">This token cannot be retrieved again after you dismiss it or leave this page.</p><label>New token<textarea className="icon-url" readOnly value={token} rows={3} onFocus={event => event.currentTarget.select()} /></label><button className="button secondary" type="button" onClick={() => setToken('')}>I saved it — dismiss token</button></section>}
    {!loaded ? <p role="status">{error ? 'API keys unavailable. Use Refresh to retry.' : 'Loading API keys…'}</p> : keys.length === 0 ? <p className="auth-help">No API keys yet.</p> : <ul className="auth-list">{keys.map(key => <li key={key.id}><div><strong>{key.name}</strong><code>{key.prefix}…</code><p className="auth-help">{key.scopes.join(', ')}{key.repositories.length > 0 && ` · ${key.repositories.join(', ')}`}</p><p className="auth-help">Created {dateLabel(key.createdAt)} · Expires {dateLabel(key.expiresAt)} · Last used {dateLabel(key.lastUsedAt)}</p></div><button className="button secondary" disabled={busy} onClick={() => {
      if (window.confirm(`Revoke API key ${key.name}? Clients using it will lose access.`)) void perform(async () => { await authRequest(`/keys/${encodeURIComponent(key.id)}`, { method: 'DELETE' }); setToken(''); await load(); setNotice('API key revoked.') })
    }}>Revoke<span className="sr-only"> {key.name}</span></button></li>)}</ul>}
    <form className="auth-form key-form" onSubmit={createKey}><h3>Create API key</h3><label>Key name<input className="icon-url" name="name" required /></label><fieldset><legend>Scopes</legend>{['services:read', 'publications:write'].map(scope => <label className="auth-checkbox" key={scope}><input name="scopes" type="checkbox" value={scope} onChange={scope === 'publications:write' ? event => setPublisher(event.target.checked) : undefined} />{scope}</label>)}</fieldset><label>Exact image repositories<textarea className="icon-url" name="repositories" rows={3} required={publisher} placeholder="ghcr.io/acme/site, ghcr.io/acme/worker" /></label><p className="auth-help">Comma or newline separated. Required for publications:write. No tags, digests or wildcards.</p><label>Expiry (optional, local time)<input className="icon-url" name="expiresAt" type="datetime-local" /></label><button className="button primary" disabled={busy || !loaded || !!token}>Create API key</button>{token && <p className="auth-help">Save and dismiss the current token before creating another.</p>}</form>
    </section><section className="mobile-account" aria-label="Signed-in administrator"><div><span>Signed in as</span><strong>{user.username}</strong></div><button className="button secondary" type="button" disabled={busy} onClick={onLogout}>Sign out</button></section></main></AppShell>
}
