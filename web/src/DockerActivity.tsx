import { useEffect, useRef, useState } from 'react'
import type { Operation } from './types.ts'
import { acknowledgeOperation } from './api.ts'

const timestamp = (value?: string) => value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleTimeString() : '—'
const phaseLabels: Record<string, string> = { preparing: 'Preparing', pulling: 'Pulling', applying: 'Applying', verifying: 'Verifying', recovering: 'Recovering' }
const stateLabel = (operation: Operation) => operation.state === 'queued' ? `Queued${operation.queuePosition ? ` #${operation.queuePosition}` : ''}` : operation.state === 'running' ? phaseLabels[operation.phase ?? ''] || 'Running' : operation.state === 'complete' ? 'Completed' : operation.phase === 'interrupted' || operation.needsAttention ? 'Interrupted' : 'Failed'

export function DockerActivity({ operations, loading, error, onAcknowledged }: { operations: Operation[]; loading: boolean; error: string; onAcknowledged?: () => void }) {
  const [acknowledging, setAcknowledging] = useState('')
  const [recoveryError, setRecoveryError] = useState('')
  const recoveryLock = useRef(false)
  const active = operations.filter(operation => operation.state === 'running' || operation.state === 'queued').sort((a, b) => (a.state === 'running' ? 0 : a.queuePosition || 1) - (b.state === 'running' ? 0 : b.queuePosition || 1))
  const history = operations.filter(operation => operation.state === 'complete' || operation.state === 'error')
  const selfUpdating = active.some(operation => operation.state === 'running' && operation.selfUpdate)
  const interruptions = operations.filter(operation => operation.needsAttention)
  const acknowledge = async (operation: Operation) => {
    if (recoveryLock.current || error || !window.confirm(`Have you checked ${operation.project} in Docker and reviewed the uncertain result? Acknowledging allows queued updates to resume. It does not retry this operation or mark it successful.`)) return
    recoveryLock.current = true
    setAcknowledging(operation.id); setRecoveryError('')
    try { await acknowledgeOperation(operation.id); onAcknowledged?.() }
    catch (cause) { setRecoveryError(cause instanceof Error ? cause.message : 'Acknowledgement failed; the queue may still be paused.') }
    finally { recoveryLock.current = false; setAcknowledging('') }
  }
  return <section className="docker-activity" aria-labelledby="docker-activity-title">
    <div className="section-title"><h2 id="docker-activity-title">Docker activity <span className="count">{error || loading ? '—' : active.length}</span></h2><span className="section-note">{error ? 'Last observed' : 'Shared across tabs · refreshes every 2s'}</span></div>
    <p className="activity-scope">Services-managed Compose actions only; commands started outside this application are not tracked.</p>
    {error && <p className={`notice ${selfUpdating ? '' : 'danger'}`} role={selfUpdating ? 'status' : 'alert'}>{selfUpdating ? 'Services is restarting or temporarily unreachable. Reconnecting automatically; last observed work is shown below and the result is not yet confirmed.' : `Activity unavailable: ${error}. Last observed work is shown below; current state is unknown.`}</p>}
    {interruptions.map(operation => <div className="notice danger" role="alert" key={operation.id}><strong>Queue paused — {operation.project}</strong><p>Check the project in Docker before acknowledging this uncertain result. Acknowledgement allows queued work to resume; it does not retry this operation or mark it successful.</p><button className="button secondary" type="button" disabled={!!error || !!acknowledging} onClick={() => void acknowledge(operation)}>{acknowledging === operation.id ? 'Checking worker…' : 'Acknowledge interruption'}</button></div>)}
    {recoveryError && <p className="notice danger" role="alert">{recoveryError}</p>}
    {!active.length && <p className="queue-empty" role="status">{error ? 'Unable to confirm whether work is running.' : loading ? 'Loading Docker activity…' : 'No Compose work running or queued.'}</p>}
    <div className="operation-list">{active.map(operation => <OperationRow key={operation.id} operation={operation} />)}</div>
    {history.length > 0 && <details className="operation-history"><summary>Recent operations <span className="count">{history.length}</span></summary><div className="operation-list">{history.map(operation => <OperationRow key={operation.id} operation={operation} />)}</div></details>}
    <p className="activity-scope">One operation at a time. Queue and recent output survive Services restarts. Independent workers continue when the dashboard disconnects. Interrupted work is checked before another update starts; uncertain results are not automatically retried.</p>
  </section>
}

function OperationRow({ operation }: { operation: Operation }) {
  const [follow, setFollow] = useState(true)
  const output = useRef<HTMLPreElement>(null)
  useEffect(() => { if (follow && output.current) output.current.scrollTop = output.current.scrollHeight }, [follow, operation.messages])
  return <details className={`operation-row ${operation.state}`}>
    <summary><span className={`status-badge ${operation.state === 'error' ? 'bad' : operation.state === 'complete' ? 'good' : 'caution'}`}>{stateLabel(operation)}</span><span className="operation-identity"><strong>{operation.project || 'Container action'}</strong><span>{operation.action}</span></span><span className="operation-time">{operation.finishedAt ? 'Finished' : operation.startedAt ? 'Started' : 'Queued'} <time>{timestamp(operation.finishedAt || operation.startedAt || operation.queuedAt)}</time></span><span className="disclosure" aria-hidden="true">›</span></summary>
    <div className="operation-inspection"><p className="operation-latest">{operation.error || operation.messages.at(-1) || 'Waiting for its turn. Docker state will be checked when the update starts.'}</p><div className="operation-output"><div className="output-heading"><h3>Command output</h3><label><input type="checkbox" checked={follow} onChange={event => setFollow(event.target.checked)} /> Follow output</label></div><pre ref={output} tabIndex={0} aria-label={`${operation.project} operation log`}>{operation.messages.join('\n') || 'No output yet.'}</pre></div><p className="operation-timestamps">Queued {timestamp(operation.queuedAt)}{operation.startedAt && ` · Started ${timestamp(operation.startedAt)}`}{operation.finishedAt && ` · Finished ${timestamp(operation.finishedAt)}`}</p></div>
  </details>
}
