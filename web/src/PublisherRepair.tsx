import { useEffect, useRef, useState } from 'react'
import { canRepairPlan } from './publisherApi.ts'
import type { Connection, createPublisherApi, PublisherStatus, RepairPlan } from './publisherApi.ts'

type Props = { connection: Connection; api: ReturnType<typeof createPublisherApi>; onStatus: (status: PublisherStatus) => void; onBusy: (busy: boolean) => void; onClose: () => void }
const message = (cause: unknown) => cause instanceof Error ? cause.message : 'Request failed. Please try again.'
const blocked = (connection: Connection) => connection.state === 'awaiting_callback' || connection.deletion?.tailscaleRevocationAttested === true || connection.deletion?.tailscaleConfirmed === true

export function PublisherRepairReview({ plan }: { plan: RepairPlan }) {
  // Never render an unvalidated provider collection, even if used independently.
  if (!canRepairPlan(plan, { repository: plan?.repository, imageRepository: plan?.imageRepository } as Connection)) return <p role="alert">The repair review is invalid. Refresh review before approving.</p>
  return <><dl className="publisher-info"><div><dt>Current repository</dt><dd>{plan.repository.fullName}</dd></div><div><dt>Current default branch</dt><dd><code>{plan.repository.defaultBranch}</code></dd></div></dl>
    <section className="publisher-repair-variables" aria-label="Exact repository variable changes">{plan.configuration.variables.map(variable => <section className="publisher-repair-variable" key={variable.name}><h3><code>{variable.name}</code><span className="publisher-badge">{!variable.exists ? 'Missing' : variable.currentValue === variable.desiredValue ? 'Unchanged' : 'Replace'}</span></h3><dl><div><dt>Current value</dt><dd>{variable.exists ? <code>{variable.currentValue === '' ? '(Empty string)' : variable.currentValue}</code> : <span>Not set</span>}</dd></div><div><dt>Desired value</dt><dd><code>{variable.desiredValue === '' ? '(Empty string)' : variable.desiredValue}</code></dd></div></dl></section>)}</section>
    <section className="publisher-info publisher-warning"><strong>{plan.configuration.secretExists ? 'Secret replacement warning' : 'Missing publisher secret'}</strong><p>{plan.configuration.secretExists ? 'The existing encrypted Actions secret SERVICES_PUBLISH_TOKEN will be replaced.' : 'The encrypted Actions secret SERVICES_PUBLISH_TOKEN will be created.'} A dedicated publisher credential is transferred server-to-server. Its value is never displayed.</p></section>
    <p className="publisher-info">Only approval applies the desired variables and publisher secret, then dispatches a fresh connectivity test. Notifications remain disabled pending an authenticated callback. Dispatch acceptance is not verification.</p><p className="publisher-muted">No image is built or published. No workflow files are changed. Review does not change remote configuration.</p></>
}

export function PublisherRepair({ connection, api, onStatus, onBusy, onClose }: Props) {
  const dialog = useRef<HTMLDialogElement>(null)
  const cancel = useRef<HTMLButtonElement>(null)
  const lock = useRef(false)
  const [plan, setPlan] = useState<RepairPlan | null>(null)
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const refresh = async (apply: boolean) => {
    if (lock.current || (apply && (!canRepairPlan(plan, connection) || blocked(connection)))) return
    lock.current = true; setBusy(true); onBusy(true); setError('')
    const reviewed = plan
    // Approval is single-use locally as well: every failure discards this review.
    setPlan(null)
    let failure = ''
    try {
      if (apply && reviewed) {
        const result = await api.repair(connection.id, reviewed.configuration.fingerprint)
        if (result?.connection?.id !== connection.id) throw new Error('Repair outcome is unconfirmed. Refresh saved status before reviewing again.')
      } else {
        const next = await api.repairPlan(connection.id)
        if (!canRepairPlan(next, connection)) throw new Error('The repair review is invalid or does not match this publisher. Refresh review before approving.')
        setPlan(next)
      }
    } catch (cause) { failure = message(cause); setPlan(null) }
    // Always read back, including HTTP/network/timeout failures with uncertain writes.
    try {
      const saved = await api.status(); onStatus(saved)
      const current = saved.connections.find(item => item.id === connection.id)
      if (!current || blocked(current)) setPlan(null)
      if (apply && !failure) {
        if (!current || !['awaiting_callback', 'verified'].includes(current.state)) throw new Error('Repair has not been confirmed by saved status. Refresh review before retrying.')
        onClose()
      }
    } catch (cause) { setPlan(null); failure += `${failure ? ' ' : ''}Saved status could not be confirmed: ${message(cause)}` }
    setError(failure)
    lock.current = false; setBusy(false); onBusy(false)
  }
  useEffect(() => {
    const previous = document.activeElement
    dialog.current?.showModal()
    void refresh(false).then(() => cancel.current?.focus())
    return () => { if (previous instanceof HTMLElement && previous.isConnected) previous.focus() }
    // Open once per mounted publisher. Refetching is always an explicit new review.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return <dialog className="publisher-confirmation publisher-repair" ref={dialog} aria-labelledby="publisher-repair-title" onCancel={event => { event.preventDefault(); if (!lock.current) onClose() }}><div className="publisher-confirmation-body"><p className="eyebrow">PUBLISHER RECOVERY</p><h2 id="publisher-repair-title">Review publisher repair</h2><p>{connection.repository.fullName} · <code>{connection.imageRepository}</code></p>
    {busy && <p role="status">Please wait — {plan ? 'applying repair' : 'refreshing saved configuration'}…</p>}
    {plan && <PublisherRepairReview plan={plan} />}
    {!plan && !busy && <p className="publisher-muted">Request a fresh review before applying repair. Previous approval is no longer available.</p>}
    {blocked(connection) && <p className="publisher-info">Repair is unavailable while awaiting a callback or after manual Tailscale revocation confirmation. Close this review and check saved status.</p>}
    {error && <p className="notice danger" role="alert">{error}</p>}
  </div><footer className="publisher-footer"><button className="button secondary" ref={cancel} disabled={busy} onClick={() => { if (!lock.current) onClose() }}>Cancel</button><button className="button secondary" disabled={busy || blocked(connection)} onClick={() => void refresh(false)}>Refresh review</button><button className="button primary" disabled={busy || !canRepairPlan(plan, connection) || blocked(connection)} onClick={() => void refresh(true)}>Apply repair &amp; test push</button></footer></dialog>
}
