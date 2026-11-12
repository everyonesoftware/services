import { useCallback, useEffect, useRef, useState } from 'react'
import { consumeWebReload, currentWebBuild, readWebBuild, rememberWebReload, webReloadCompleted } from './webAppUpdate.ts'
import './WebAppUpdates.css'

type Status = 'idle' | 'checking' | 'available' | 'current' | 'updated' | 'offline' | 'error' | 'reload-error' | 'reloading'
const copy: Record<Status, [string, string]> = {
  idle: ['Check when you’re ready', 'The app will not reload without your confirmation.'],
  checking: ['Checking for updates…', 'Looking for a newer web app build.'],
  available: ['Update available', 'A newer web app build is ready. Reload now or keep working and update later.'],
  current: ['You’re up to date', 'You’re using the latest web app build.'],
  updated: ['Web app updated', 'You’re now using the new web app build.'],
  offline: ['You’re offline', 'Connect to the internet, then check again. The current web app has not changed.'],
  error: ['Couldn’t check for updates', 'Please try again. The current web app has not changed.'],
  'reload-error': ['Couldn’t reload the web app', 'Your update is still available; try again when you’re ready.'],
  reloading: ['Reloading web app…', 'Loading the latest web app build.'],
}

export function WebAppUpdates({ hidden = false, disabled = false }: { hidden?: boolean; disabled?: boolean }) {
  const [current] = useState(() => typeof document === 'undefined' ? null : currentWebBuild(document))
  const [status, setStatus] = useState<Status>(() => {
    try { return current && webReloadCompleted(window.sessionStorage, current) ? 'updated' : 'idle' } catch { return 'idle' }
  })
  const [latest, setLatest] = useState<string | null>(null)
  const [checkedAt, setCheckedAt] = useState<Date | null>(null)
  const [failure, setFailure] = useState('')
  const dialog = useRef<HTMLDialogElement>(null)
  const later = useRef<HTMLButtonElement>(null)
  const apply = useRef<HTMLButtonElement>(null)
  const check = useRef<HTMLButtonElement>(null)
  const reviewed = useRef<string | null>(null)
  const lock = useRef(false)
  const sequence = useRef(0)
  const request = useRef<AbortController | null>(null)
  const cancelRequest = useCallback(() => { sequence.current++; request.current?.abort() }, [])
  const busy = status === 'checking' || status === 'reloading'
  const available = status === 'available' || status === 'reload-error'

  useEffect(() => {
    try { if (current) consumeWebReload(window.sessionStorage, current) } catch { /* Storage may be disabled. */ }
    return cancelRequest
  }, [current, cancelRequest])

  const run = async (target?: string) => {
    if (lock.current || disabled || !current) return
    setFailure('')
    if (!navigator.onLine) { setStatus('offline'); return }
    lock.current = true
    const version = ++sequence.current
    const controller = new AbortController()
    request.current = controller
    setStatus(target ? 'reloading' : 'checking')
    try {
      const id = await readWebBuild(fetch, { signal: controller.signal })
      if (sequence.current !== version) return
      setCheckedAt(new Date())
      setLatest(id === current ? null : id)
      if (target && id === target) {
        // Network-only navigation loads fresh HTML/assets. Never mutate Docker or force a worker takeover.
        try { rememberWebReload(window.sessionStorage, target) } catch { /* Reload still works without storage. */ }
        window.location.reload()
      } else {
        setStatus(id === current ? 'current' : 'available')
        if (target && id !== current) setFailure('A different web app build is now available. Review it again before reloading.')
      }
    } catch (cause) {
      if (sequence.current !== version) return
      setStatus(!navigator.onLine ? 'offline' : target ? 'reload-error' : 'error')
      setFailure(cause instanceof Error ? cause.message : 'The update check failed. Please try again.')
    } finally {
      if (sequence.current === version) { lock.current = false; request.current = null }
    }
  }
  const review = () => {
    if (busy || disabled || !latest) return
    reviewed.current = latest
    dialog.current?.showModal()
    later.current?.focus()
  }
  const restoreFocus = () => {
    reviewed.current = null
    if (apply.current && !apply.current.hidden) apply.current.focus()
    else check.current?.focus()
  }
  const [title, description] = copy[status]
  return <section className="auth-card webapp-update-card" aria-labelledby="webapp-updates-title" hidden={hidden}>
    <h2 id="webapp-updates-title">Web app updates</h2>
    <p className="auth-help">Check for the latest version of the Services web app.</p>
    <dl className="webapp-version-details">
      <div><dt>Current build</dt><dd title={current ?? undefined}>{current ? current.slice(0, 12) : 'Development build'}</dd></div>
      <div><dt>Last checked</dt><dd>{checkedAt ? checkedAt.toLocaleString() : 'Not checked'}</dd></div>
    </dl>
    <div className="webapp-update-result" role="status" aria-live="polite" aria-atomic="true" data-state={status}>
      <h3>{current ? title : 'Production build required'}</h3>
      <p className="auth-help">{current ? description : 'Update checks are available in a production build of the web app.'}</p>
      {failure && <p className="webapp-update-error">{failure}</p>}
    </div>
    <div className="webapp-update-actions">
      <button ref={check} className="button secondary" type="button" disabled={disabled || busy || !current} onClick={() => void run()}>{status === 'checking' ? 'Checking…' : 'Check for updates'}</button>
      <button ref={apply} className="button primary" type="button" hidden={!available} disabled={disabled || busy} onClick={review}>Update and reload</button>
    </div>
    <p className="webapp-update-help">Updates apply to the web app on this device. You choose when to reload.</p>
    <dialog ref={dialog} className="webapp-reload-dialog" aria-labelledby="webapp-reload-title" aria-describedby="webapp-reload-description webapp-reload-warning" onClose={restoreFocus} onClick={event => {
      if (event.target !== event.currentTarget) return
      const bounds = event.currentTarget.getBoundingClientRect()
      if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) dialog.current?.close()
    }}>
      <header><h2 id="webapp-reload-title">Reload to update?</h2></header>
      <div className="webapp-reload-body">
        <p id="webapp-reload-description">A newer version of the Services web app is ready. Reload to start using it.</p>
        <p className="webapp-reload-warning" id="webapp-reload-warning">Finish or save any unsaved changes before reloading. Your current view will close.</p>
      </div>
      <footer><button ref={later} className="button secondary" type="button" onClick={() => dialog.current?.close()}>Later</button><button className="button primary" type="button" onClick={() => {
        const target = reviewed.current
        dialog.current?.close()
        if (target) void run(target)
      }}>Update and reload</button></footer>
    </dialog>
  </section>
}
