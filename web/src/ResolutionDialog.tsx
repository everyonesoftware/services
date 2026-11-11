import { useEffect, useRef, useState } from 'react'
import { resolveIncidents } from './api.ts'
import { applyResolutionAction, initialResolutionDrafts, validateResolution } from './resolution.ts'
import type { ResolutionDraft, ResolutionEntry } from './resolution.ts'
import { isSnoozed } from './monitor.ts'

const dateLabel = (value: string) => Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : value
function Choices() {
  return <><option value="">Choose a resolution…</option><option value="replace">Renamed or replaced</option><option value="forget">Intentionally removed</option><option value="snooze">Not sure yet · Snooze alerts</option></>
}

export type ResolutionTarget = { entries: ResolutionEntry[]; trigger: HTMLElement | null }
export function ResolutionDialog({ target, connected, onClose, onPending, onResolved }: { target: ResolutionTarget; connected: boolean; onClose: () => void; onPending: (pending: boolean) => void; onResolved: (token: string) => void }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const cancel = useRef<HTMLButtonElement>(null)
  const lock = useRef(false)
  const [drafts, setDrafts] = useState(() => initialResolutionDrafts(target.entries))
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState('')
  const { entries } = target
  const bulk = entries.length > 1
  const validation = validateResolution(entries, drafts)
  const selected = entries.filter(entry => drafts[entry.incident.id].included).length
  const change = (id: string, changes: Partial<ResolutionDraft>) => setDrafts(current => ({ ...current, [id]: { ...current[id], ...changes } }))
  useEffect(() => {
    dialog.current?.showModal()
    cancel.current?.focus()
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = previousOverflow; if (target.trigger?.isConnected) target.trigger.focus() }
  }, [target])
  const dismiss = () => { if (!lock.current) { dialog.current?.close(); onClose() } }
  const submit = async () => {
    if (lock.current || !connected || !validation.valid) return
    lock.current = true; setBusy(true); setFailure(''); onPending(true)
    try {
      const result = await resolveIncidents(validation.decisions)
      onResolved(result.undoToken)
      lock.current = false
      dialog.current?.close()
      onClose()
    } catch (cause) { setFailure(cause instanceof Error ? cause.message : 'Resolution failed. Your choices have been kept.') }
    finally { lock.current = false; setBusy(false); onPending(false) }
  }
  return <dialog ref={dialog} className="update-dialog resolution-dialog" aria-labelledby="resolution-title" aria-describedby="resolution-scope" aria-busy={busy} onCancel={event => { event.preventDefault(); dismiss() }} onKeyDown={event => {
    if (event.key !== 'Tab') return
    const controls = [...event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled), select:not(:disabled), input:not(:disabled), summary')].filter(control => control.getClientRects().length)
    const first = controls[0], last = controls.at(-1)
    if (!first) { event.preventDefault(); return }
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
  }}>
    <form onSubmit={event => { event.preventDefault(); void submit() }}>
      <header className="dialog-header"><span className="eyebrow">MISSING SERVICES</span><h2 id="resolution-title">{bulk ? `Resolve ${entries.length === 2 ? 'both' : 'all'} missing entries` : 'Resolve missing entry'}</h2><p>Review what changed, then choose how to handle {bulk ? 'each entry' : 'this entry'}. Nothing changes until you apply.</p></header>
      <div className="dialog-scroll resolution-body">
        {bulk && <div className="resolution-bulk"><strong>{entries.length} missing entries</strong><label>Apply to all selected<select aria-label="Apply to all selected" value="" disabled={busy} onChange={event => setDrafts(current => applyResolutionAction(current, event.target.value as ResolutionDraft['action']))}><Choices /></select></label></div>}
        {entries.map((entry, index) => {
          const { incident, previous, suggestion, candidates, events } = entry
          const draft = drafts[incident.id]
          const prefix = `resolution-${index}`
          const snoozed = isSnoozed(incident.snoozedUntil)
          return <section key={incident.id} className={`resolution-entry ${draft.included ? '' : 'excluded'}`} role="region" aria-label={incident.name}>
            <div className="resolution-entry-head">{bulk && <input type="checkbox" aria-label={`Include ${incident.name}`} checked={draft.included} disabled={busy} onChange={event => change(incident.id, { included: event.target.checked })} />}<div><h3>{incident.name}</h3><small>Missing since <time dateTime={incident.since}>{dateLabel(incident.since)}</time> · {incident.project}</small></div><span className="status-badge bad">{snoozed ? 'Missing · Snoozed' : 'Missing'}</span></div>
            <div className="resolution-context"><p>{incident.reason || 'Previously observed container disappeared.'}</p>{snoozed && <p>Alerts snoozed until {dateLabel(incident.snoozedUntil!)}.</p>}<dl className="resolution-facts"><div><dt>Project</dt><dd>{incident.project}</dd></div><div><dt>Previously used image</dt><dd><code>{previous?.image || 'Not reported'}</code></dd></div></dl>
              {suggestion && <p className="resolution-suggestion">Possible replacement: <strong>{suggestion.name}</strong><br /><small>Same project and image reference. Not proof of a rename.</small></p>}
              <details><summary>Observation details</summary>{events.length ? <ol className="resolution-events">{events.map(event => <li key={event.id}><time dateTime={event.time}>{dateLabel(event.time)}</time><p>{event.message}</p></li>)}</ol> : <p>No retained events for this entry.</p>}<p>The old container is absent; live logs and restart controls are unavailable. Inspect the deployment on the Docker host if this was unexpected.</p></details>
            </div>
            {draft.included && <div className="resolution-decision"><label htmlFor={`${prefix}-action`}>How should Services handle this?</label><select id={`${prefix}-action`} value={draft.action} disabled={busy} onChange={event => change(incident.id, { action: event.target.value as ResolutionDraft['action'] })}><Choices /></select>
              {draft.action === 'replace' && <div className="resolution-fields"><label htmlFor={`${prefix}-replacement`}>Replaced by</label><select id={`${prefix}-replacement`} value={draft.replacementId} disabled={busy} aria-invalid={!!validation.errors[incident.id]} aria-describedby={`${prefix}-error`} onChange={event => change(incident.id, { replacementId: event.target.value })}><option value="">Select a current container…</option>{candidates.map(c => <option key={c.id} value={c.id}>{c.project} / {c.name} · {c.monitorStatus || c.health || c.state}</option>)}</select><p>Record the relationship and clear the old missing entry. This does not rename a Docker container.</p></div>}
              {draft.action === 'forget' && <p>Forget the old monitoring entry and clear its incident. It may be rediscovered if it returns.</p>}
              {draft.action === 'snooze' && <div className="resolution-fields"><label htmlFor={`${prefix}-duration`}>Pause alerts for</label><select id={`${prefix}-duration`} value={draft.snoozeSeconds} disabled={busy} onChange={event => change(incident.id, { snoozeSeconds: Number(event.target.value) as ResolutionDraft['snoozeSeconds'] })}><option value={900}>15 minutes</option><option value={3600}>1 hour</option><option value={86400}>24 hours</option></select><p>This entry stays missing. Other services’ alerts are unaffected.</p></div>}
              <p id={`${prefix}-error`} className="resolution-inline-error" role="alert">{draft.action && validation.errors[incident.id]}</p>
            </div>}
          </section>
        })}
        <p id="resolution-scope" className="resolution-scope"><strong>Monitoring only.</strong> No Docker containers, files or volumes will be changed. Snoozed entries stay missing; alerts resume after the chosen duration. Resolution history, snoozes and Undo are held in memory and reset when Services restarts.</p>
      </div>
      <footer className="dialog-actions"><p role="alert" className="resolution-error">{failure || validation.errors.selection || (!connected ? 'Monitoring is disconnected or stale. Close this dialog and refresh before applying.' : '')}</p><span className="resolution-selection">{bulk ? `${selected} of ${entries.length} selected` : '1 missing entry'}</span><button ref={cancel} type="button" className="button secondary" disabled={busy} onClick={dismiss}>Cancel</button><button type="submit" className="button primary" disabled={busy || !connected || !validation.valid}>{busy ? 'Applying…' : selected > 1 ? `Apply ${selected} decisions` : 'Apply resolution'}</button></footer>
    </form>
  </dialog>
}
