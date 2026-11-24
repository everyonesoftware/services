import { useEffect, useLayoutEffect, useState } from 'react'
import type { FormEvent } from 'react'
import App from './App.tsx'
import { AuthAccounts } from './AuthAccounts.tsx'
import { Publishers } from './Publishers.tsx'
import type { AppPage } from './Publishers.tsx'
import { AuthError, authRequest, sessionExpiredEvent, sessionFetch } from './authApi.ts'
import type { AuthUser } from './authApi.ts'
import './App.css'

const message = (cause: unknown) => cause instanceof Error ? cause.message : 'Request failed. Please try again.'

export function AuthForm({ setup, onAuthenticated }: { setup: boolean; onAuthenticated: (user: AuthUser) => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    const body = { username: String(data.get('username')), password: String(data.get('password')), ...(setup ? { setupToken: String(data.get('setupToken')) } : {}) }
    setBusy(true); setError('')
    try { const result = await authRequest<{ user: AuthUser }>(setup ? '/setup' : '/login', { method: 'POST', body }); form.reset(); onAuthenticated(result.user) }
    catch (cause) { form.querySelector<HTMLInputElement>('[name=password]')!.value = ''; setError(message(cause)) }
    finally { setBusy(false) }
  }
  return <main className="auth-shell"><section className="auth-card" aria-labelledby="auth-title"><p className="eyebrow">SERVICES</p><h1 id="auth-title">{setup ? 'Create your first administrator' : 'Sign in'}</h1><p className="auth-help">{setup ? <>Read the local setup token from <code>/data/admin-setup-token</code> on the Services server, then enter it below. Do not share this token.</> : 'Sign in with your Services administrator account.'}</p><form className="auth-form" onSubmit={event => void submit(event)}>
    <label>Username<input className="icon-url" name="username" autoComplete="username" required autoFocus /></label>
    <label>Password<input className="icon-url" name="password" type="password" autoComplete={setup ? 'new-password' : 'current-password'} minLength={setup ? 15 : undefined} maxLength={1024} required /></label>
    {setup && <><p className="auth-help">At least 15 Unicode characters; at most 1024 UTF-8 bytes.</p><label>Setup token<input className="icon-url" name="setupToken" type="password" autoComplete="off" required /></label></>}
    {error && <p className="notice danger" role="alert">{error}</p>}
    <button className="button primary" disabled={busy} type="submit">{busy ? 'Please wait…' : setup ? 'Create administrator' : 'Sign in'}</button>
  </form></section></main>
}

export function AuthGate() {
  const [user, setUser] = useState<AuthUser | null>(null)
  const pageFromHash = (): AppPage => {
    const hash = typeof window === 'undefined' ? '' : window.location.hash
    return hash === '#activity' ? 'activity' : hash === '#publishers' || hash.startsWith('#publishers/') ? 'publishers' : hash === '#settings' || hash === '#accounts' ? 'settings' : 'monitor'
  }
  const [page, setPage] = useState<AppPage>(pageFromHash)
  const navigate = (next: AppPage) => { window.location.hash = next; setPage(next) }
  useEffect(() => {
    const changed = () => setPage(pageFromHash())
    window.addEventListener('hashchange', changed)
    return () => window.removeEventListener('hashchange', changed)
  }, [])
  const [setup, setSetup] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    let active = true
    const load = async () => {
      setLoading(true); setError('')
      try {
        const status = await authRequest<{ setupRequired: boolean }>('/status')
        if (!active) return
        setSetup(status.setupRequired)
        if (!status.setupRequired) {
          try { const result = await authRequest<{ user: AuthUser }>('/me'); if (active) setUser(result.user) }
          catch (cause) { if (!(cause instanceof AuthError && cause.status === 401)) throw cause }
        }
      } catch (cause) { if (active) setError(message(cause)) }
      finally { if (active) setLoading(false) }
    }
    void load()
    return () => { active = false }
  }, [revision])
  useLayoutEffect(() => {
    if (!user) return
    const original = window.fetch
    let active = true
    const expired = () => { setUser(null); setPage('monitor'); setError(''); setSetup(false) }
    window.addEventListener(sessionExpiredEvent, expired)
    // Install before descendant polling effects; ignore late responses from an old session.
    window.fetch = sessionFetch(original, () => { if (active) window.dispatchEvent(new Event(sessionExpiredEvent)) }, window.location.origin)
    return () => { active = false; window.fetch = original; window.removeEventListener(sessionExpiredEvent, expired) }
  }, [user])
  const logout = async () => {
    await authRequest('/logout', { method: 'POST', body: {} })
    setUser(null); setPage('monitor'); setError(''); setSetup(false)
  }
  if (loading) return <main className="auth-shell" role="status">Checking session…</main>
  if (error && !user) return <main className="auth-shell"><section className="auth-card"><p role="alert" className="notice danger">{error}</p><button className="button secondary" onClick={() => setRevision(value => value + 1)}>Try again</button></section></main>
  if (!user) return <AuthForm key={setup ? 'setup' : 'login'} setup={setup} onAuthenticated={setUser} />
  return page === 'settings' ? <AuthAccounts user={user} onBack={() => navigate('monitor')} onPublishers={() => navigate('publishers')} onLogout={logout} onPasswordChanged={() => { setUser(null); navigate('monitor'); setSetup(false) }} /> : page === 'publishers' ? <Publishers user={user} onBack={() => navigate('monitor')} onAccounts={() => navigate('settings')} /> : <App activePage={page} user={user} onAccounts={() => navigate('settings')} onPublishers={() => navigate('publishers')} />
}
