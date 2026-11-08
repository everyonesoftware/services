import { useEffect, useRef, useState } from 'react'
import type { Operation } from './types.ts'

const timestamp = (value?: string) => value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleTimeString() : '—'
const stateLabel = (operation: Operation) => operation.state === 'queued' ? `Queued${operation.queuePosition ? ` #${operation.queuePosition}` : ''}` : operation.state === 'running' ? 'Running' : operation.state === 'complete' ? 'Completed' : 'Failed'

export function DockerActivity({ operations, loading, error }: { operations: Operation[]; loading: boolean; error: string }) {
  const active = operations.filter(operation => operation.state === 'running' || operation.state === 'queued').sort((a, b) => (a.state === 'running' ? 0 : a.queuePosition || 1) - (b.state === 'running' ? 0 : b.queuePosition || 1))
  const history = operations.filter(operation => operation.state === 'complete' || operation.state === 'error')
  return <section className="docker-activity" aria-labelledby="docker-activity-title">
    <div className="section-title"><h2 id="docker-activity-title">Docker activity <span className="count">{error || loading ? '—' : active.length}</span></h2><span className="section-note">{error ? 'Last observed' : 'Shared across tabs · refreshes every 2s'}</span></div>
    <p className="activity-scope">Services-managed Compose actions only; commands started outside this application are not tracked.</p>
    {error && <p className="notice danger" role="alert">Activity unavailable: {error}. Last observed work is shown below; current state is unknown.</p>}
    {!active.length && <p className="queue-empty" role="status">{error ? 'Unable to confirm whether work is running.' : loading ? 'Loading Docker activity…' : 'No Compose work running or queued.'}</p>}
    <div className="operation-list">{active.map(operation => <OperationRow key={operation.id} operation={operation} />)}</div>
    {history.length > 0 && <details className="operation-history"><summary>Recent operations <span className="count">{history.length}</span></summary><div className="operation-list">{history.map(operation => <OperationRow key={operation.id} operation={operation} />)}</div></details>}
    <p className="activity-scope">One operation at a time. Queued updates continue after you close a tab. Queue and recent output reset when Services restarts.</p>
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
