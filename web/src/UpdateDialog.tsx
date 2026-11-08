import { useEffect, useRef, useState } from 'react'
import { updateProject } from './api.ts'
import { availableServices, canUpdateProject } from './monitor.ts'
import type { Project } from './types.ts'

export function UpdateDialog({ project, onClose, onUpdated }: { project: Project; onClose: () => void; onUpdated: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const log = useRef<HTMLPreElement>(null)
  const lock = useRef(false)
  const [phase, setPhase] = useState<'review' | 'running' | 'complete' | 'error'>('review')
  const [messages, setMessages] = useState<string[]>([])
  const [failure, setFailure] = useState('')
  const [tail, setTail] = useState(true)
  const services = availableServices(project)
  const busy = phase === 'running'
  useEffect(() => { if (!dialog.current?.open) dialog.current?.showModal() }, [])
  useEffect(() => { if (tail && log.current) log.current.scrollTop = log.current.scrollHeight }, [messages, tail])
  const dismiss = () => { if (!lock.current) { dialog.current?.close(); onClose() } }
  const run = async () => {
    if (lock.current || phase !== 'review' || !canUpdateProject(project)) return
    lock.current = true
    setPhase('running')
    try {
      await updateProject(project.name, event => setMessages(previous => [...previous, event.message]))
      setPhase('complete')
    } catch (cause) {
      setFailure(cause instanceof Error ? cause.message : 'The update could not be completed.')
      setPhase('error')
    } finally { lock.current = false; onUpdated() }
  }
  return <dialog ref={dialog} className="update-dialog" aria-labelledby="update-title" aria-describedby="update-warning" onCancel={event => { event.preventDefault(); dismiss() }}>
    <div className="dialog-header"><span className="eyebrow">COMPOSE GROUP UPDATE</span><h2 id="update-title">Review updates <span>/ {project.name}</span></h2><p>{services.length} {services.length === 1 ? 'service' : 'services'} with new images. One group operation.</p></div>
    <div className="dialog-scroll"><div className="update-warning" id="update-warning"><strong>Containers will be recreated. Expect downtime.</strong><p>This updates all replicas of the services below using the current Compose configuration and environment. Other services are not selected. Image tags can change after this check; digests are the last observed values.</p></div>
      <div className="review-services">{services.map(service => <section className="review-service" key={service.service}><div className="review-service-title"><h3>{service.service}</h3><span>{service.replicas} {service.replicas === 1 ? 'replica' : 'replicas'}</span></div><code className="review-image">{service.image}</code><div className="digest-comparison"><div><span>CURRENT DIGEST</span>{service.currentDigests.map(digest => <code key={digest}>{digest}</code>)}</div><div><span>PROPOSED DIGEST</span>{service.proposedDigests.map(digest => <code key={digest}>{digest}</code>)}</div></div></section>)}</div>
      {phase !== 'review' && <section className="operation-output" aria-label="Update progress"><div className="output-heading"><h3>{busy ? 'Updating services…' : phase === 'complete' ? 'Update completed' : 'Update not confirmed'}</h3><label><input type="checkbox" checked={tail} onChange={event => setTail(event.target.checked)} /> Follow output</label></div><pre ref={log} tabIndex={0} aria-label="Operation log">{messages.length ? messages.join('\n') : 'Waiting for command output…'}</pre><p className={`operation-result ${phase}`} role={phase === 'error' ? 'alert' : 'status'}>{busy ? 'Keep this window open while the operation runs.' : phase === 'complete' ? 'The engine confirmed completion. Refreshing service health.' : `${failure} Check current service health before starting another update.`}</p></section>}
    </div>
    <div className="dialog-actions"><span>{busy ? 'Operation in progress' : phase === 'review' ? 'Review the scope before continuing.' : 'Service health is being refreshed.'}</span><button type="button" className="button secondary" autoFocus onClick={dismiss} disabled={busy}>{phase === 'review' ? 'Cancel' : 'Close'}</button>{(phase === 'review' || busy) && <button type="button" className="button primary" disabled={busy || !canUpdateProject(project)} onClick={() => void run()}>{busy ? 'Updating…' : 'Confirm & update'}</button>}</div>
  </dialog>
}
