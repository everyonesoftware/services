import { useEffect, useRef, useState } from 'react'
import { canReconcilePlan } from './publisherApi.ts'
import type { Connection, createPublisherApi, PublisherStatus, ReconciliationPlan } from './publisherApi.ts'
import { publisherHref, publisherNeedsAttention, publisherSignal } from './publisherReconciliation.ts'
import './PublisherReconciliation.css'

export function PublisherLinks({ connections }: { connections: Connection[] }) {
  return connections.length > 0 ? <div className="publisher-signals">{connections.map(connection => <a className={`publisher-signal${publisherNeedsAttention(connection) ? ' attention' : ''}`} key={connection.id} href={publisherHref(connection.id)}>{publisherSignal(connection)}</a>)}</div> : null
}
export function PublisherRenameNotice({ connection, busy, onReview, onRefresh }: { connection: Connection; busy: boolean; onReview: () => void; onRefresh: () => void }) {
  const review = connection.reconciliation
  if (!review) return null
  const renamed = review.previousName !== review.currentName
  const title = { review_required: renamed ? 'Repository rename detected' : 'Repository identity review required', identity_mismatch: 'Repository identity mismatch', unavailable: 'Repository identity unavailable', awaiting_publication: 'Awaiting next publication', reconciled: renamed ? 'Repository rename reconciled' : 'Repository identity reconciled' }[review.state]
  return <section className={`publisher-rename-notice${publisherNeedsAttention(connection) ? ' attention' : ''}`} aria-label="Repository reconciliation"><strong>{title}</strong><p>{review.message}</p>{review.state === 'awaiting_publication' && <p>Connectivity callbacks do not confirm image delivery. Awaiting a real publication receipt.</p>}<div className="publisher-rename-actions">{['review_required', 'unavailable'].includes(review.state) && <button className="button primary" disabled={busy || connection.state === 'disconnected' || !!connection.deletion} onClick={onReview}>{review.state === 'review_required' && renamed ? 'Review rename' : 'Review identity'}</button>}<button className="button secondary" disabled={busy} onClick={onRefresh}>Refresh status</button></div></section>
}

type Props = { connection: Connection; api: ReturnType<typeof createPublisherApi>; onStatus: (status: PublisherStatus) => void; onBusy: (busy: boolean) => void; onClose: () => void }
const message = (cause: unknown) => cause instanceof Error ? cause.message : 'Request failed. Please try again.'
export function PublisherReconciliation({ connection, api, onStatus, onBusy, onClose }: Props) {
  const dialog = useRef<HTMLDialogElement>(null)
  const cancel = useRef<HTMLButtonElement>(null)
  const opener = useRef<HTMLElement | null>(null)
  const lock = useRef(false)
  const [plan, setPlan] = useState<ReconciliationPlan | null>(null)
  const [approved, setApproved] = useState(false)
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  useEffect(() => {
    if (!opener.current && document.activeElement instanceof HTMLElement && !dialog.current?.contains(document.activeElement)) opener.current = document.activeElement
    let active = true
    dialog.current?.showModal()
    const load = async () => {
      try {
        const next = await api.reconciliationPlan(connection.id)
        const saved = await api.status()
        if (!active) return
        onStatus(saved)
        const current = saved.connections.find(item => item.id === connection.id)
        if (!current || !canReconcilePlan(next, current)) throw new Error('This repository identity review is invalid or no longer matches this publisher. Close and refresh status before reviewing again.')
        setPlan(next)
      } catch (cause) { if (active) setError(message(cause)) }
      finally { if (active) { setBusy(false); cancel.current?.focus() } }
    }
    void load()
    const modal = dialog.current
    return () => {
      active = false; modal?.close()
      if (opener.current?.isConnected) opener.current.focus()
      else {
        const notice = document.getElementById(`publisher-${encodeURIComponent(connection.id)}-details`)?.querySelector<HTMLElement>('.publisher-rename-notice')
        if (notice) { notice.tabIndex = -1; notice.focus({ preventScroll: true }) }
      }
    }
    // Review is a single-use snapshot, fetched once per dialog opening.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const apply = async () => {
    if (lock.current || busy || !approved || !canReconcilePlan(plan, connection) || !plan) return
    lock.current = true; setBusy(true); onBusy(true); setError('')
    dialog.current?.focus()
    const reviewed = plan
    setPlan(null); setApproved(false)
    let failure = ''
    try {
      const result = await api.reconcile(connection.id, reviewed.fingerprint)
      if (result?.connection?.id !== connection.id) throw new Error('Repository identity outcome is unconfirmed.')
    } catch (cause) { failure = message(cause) }
    try {
      const saved = await api.status(); onStatus(saved)
      const current = saved.connections.find(item => item.id === connection.id)
      if (!failure) {
        if (!current || current.repository.id !== reviewed.repository.id || current.repository.installationId !== reviewed.repository.installationId || current.imageRepository !== reviewed.imageRepository || !['awaiting_publication', 'reconciled'].includes(current.reconciliation?.state ?? '')) throw new Error('Repository identity reconciliation has not been confirmed by saved status.')
        onClose()
      }
    } catch (cause) { failure += `${failure ? ' ' : ''}Saved status could not be confirmed: ${message(cause)}` }
    setError(failure); lock.current = false; setBusy(false); onBusy(false)
  }
  const valid = canReconcilePlan(plan, connection)
  const renamed = plan ? plan.previousName !== plan.repository.fullName : connection.reconciliation?.state === 'review_required' && connection.reconciliation.previousName !== connection.reconciliation.currentName
  return <dialog ref={dialog} tabIndex={-1} className="publisher-confirmation publisher-rename-dialog" aria-labelledby="publisher-rename-title" onKeyDown={event => {
    if (event.key !== 'Tab') return
    const controls = [...event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)')]
    const first = controls[0], last = controls.at(-1)
    if (!first) { event.preventDefault(); event.currentTarget.focus() }
    else if (event.shiftKey && (document.activeElement === first || !controls.includes(document.activeElement as HTMLElement))) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && (document.activeElement === last || !controls.includes(document.activeElement as HTMLElement))) { event.preventDefault(); first.focus() }
  }} onCancel={event => { event.preventDefault(); if (!lock.current) onClose() }}><div className="publisher-confirmation-body"><h2 id="publisher-rename-title">{renamed ? 'Review repository rename' : 'Review repository identity'}</h2>
    {busy && <p role="status">{lock.current ? 'Applying repository identity and checking saved status…' : 'Loading current review…'}</p>}
    {plan && valid && <><dl className="rename-facts"><div><dt>Previous name</dt><dd><code>{plan.previousName}</code></dd></div><div><dt>Current name</dt><dd><code>{plan.repository.fullName}</code></dd></div><div><dt>Immutable repository ID</dt><dd><code>{plan.repository.id}</code></dd></div><div><dt>Image scope</dt><dd><code>{plan.imageRepository}</code></dd></div></dl><p>History and replay protection stay intact. No credential or workflow changes.</p><label className="rename-approval"><input type="checkbox" checked={approved} onChange={event => setApproved(event.target.checked)} /><span>I approve binding this image’s history to the verified repository ID.</span></label></>}
    {(!valid && !busy && !error) && <p role="alert">The review is no longer valid. Close and refresh status before reviewing again.</p>}
    {error && <p className="notice danger" role="alert">{error}</p>}
  </div><footer className="publisher-footer"><button ref={cancel} className="button secondary" disabled={lock.current} onClick={onClose}>Cancel</button><button className="button primary" disabled={busy || !valid || !approved} onClick={() => void apply()}>Apply</button></footer></dialog>
}
