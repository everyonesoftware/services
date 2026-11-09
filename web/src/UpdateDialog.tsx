import { useEffect, useRef, useState } from 'react'
import { updateProject } from './api.ts'
import { availableServices, canUpdateProject } from './monitor.ts'
import { PublicationDetails } from './PublicationDetails.tsx'
import type { Operation, Project } from './types.ts'

export function UpdateDialog({ project, onClose, onUpdated, engineBusy = false }: { project: Project; onClose: () => void; onUpdated: (operation?: Operation) => void; engineBusy?: boolean }) {
  const dialog = useRef<HTMLDialogElement>(null)

  const lock = useRef(false)
  const [phase, setPhase] = useState<'review' | 'submitting' | 'accepted' | 'error'>('review')
  const [accepted, setAccepted] = useState<Operation | null>(null)
  const [failure, setFailure] = useState('')

  const services = availableServices(project)
  const busy = phase === 'submitting'
  useEffect(() => { if (!dialog.current?.open) dialog.current?.showModal() }, [])

  const dismiss = () => { if (!lock.current) { dialog.current?.close(); onClose() } }
  const run = async () => {
    if (lock.current || phase !== 'review' || !canUpdateProject(project)) return
    lock.current = true
    setPhase('submitting')
    try {
      const operation = await updateProject(project.name)
      setAccepted(operation)
      setPhase('accepted')
      onUpdated(operation)
    } catch (cause) {
      setFailure(cause instanceof Error ? cause.message : 'The update could not be completed.')
      setPhase('error')
    } finally { lock.current = false }
  }
  return <dialog ref={dialog} className="update-dialog" aria-labelledby="update-title" aria-describedby="update-warning" onCancel={event => { event.preventDefault(); dismiss() }}>
    <div className="dialog-header"><span className="eyebrow">COMPOSE GROUP UPDATE</span><h2 id="update-title">Review updates <span>/ {project.name}</span></h2><p>{services.length} {services.length === 1 ? 'service' : 'services'} with new images. One group operation.</p></div>
    <div className="dialog-scroll"><div className="update-warning" id="update-warning"><strong>Containers will be recreated. Expect downtime.</strong><p>This updates the selected services below using the current Compose configuration and environment. Running network-sharing dependents may also be recreated, including this dashboard; intentionally stopped services stay stopped. Image tags can change after this check; digests are the last observed values.</p></div>
      <div className="review-services">{services.map(service => <section className="review-service" key={service.service}><div className="review-service-title"><h3>{service.service}</h3><span>{service.replicas} {service.replicas === 1 ? 'replica' : 'replicas'}</span></div><code className="review-image">{service.image}</code><PublicationDetails containers={project.containers.filter(container => (container.service || container.name || container.id) === service.service)} /><div className="digest-comparison"><div><span>CURRENT DIGEST</span>{service.currentDigests.map(digest => <code key={digest}>{digest}</code>)}</div><div><span>PROPOSED DIGEST</span>{service.proposedDigests.map(digest => <code key={digest}>{digest}</code>)}</div></div></section>)}</div>
      <p className="queue-explanation">Updates run one at a time in an independent worker. Docker state and available updates are re-checked before execution. Once accepted, the queue and recent output survive Services restarts. You can close this dialog or tab and follow progress in Docker activity. A Services update briefly disconnects this dashboard; it reconnects automatically without submitting the update again.</p>
      {phase !== 'review' && <p className={`notice ${phase === 'error' ? 'danger' : ''}`} role={phase === 'error' ? 'alert' : 'status'}>{busy ? 'Submitting update…' : phase === 'accepted' ? `Update accepted${accepted?.state === 'queued' ? ' into the queue' : ''}. Follow its current state and output in Docker activity. You can close this dialog and queue another project.` : failure}</p>}
    </div>
    <div className="dialog-actions"><span>{busy ? 'Waiting for acceptance' : phase === 'review' ? engineBusy ? 'Docker is busy. This update will wait its turn.' : 'Review the scope before continuing.' : 'Track updates in Docker activity.'}</span><button type="button" className="button secondary" autoFocus onClick={dismiss} disabled={busy}>{phase === 'review' ? 'Cancel' : 'Close'}</button>{(phase === 'review' || busy) && <button type="button" className="button primary" disabled={busy || !canUpdateProject(project)} onClick={() => void run()}>{busy ? 'Submitting…' : engineBusy ? 'Confirm & queue' : 'Confirm & update'}</button>}</div>
  </dialog>
}
