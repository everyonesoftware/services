import { useCallback, useEffect, useRef, useState } from 'react'
import { checkForUpdates } from './api.ts'
import { availableServices, connectionState, needsAttention, projectsWithOperations, recentEvents, selectProjects } from './monitor.ts'
import type { Filter, IconList, Monitor, Operation, Project } from './types.ts'
import { IconDialog } from './IconDialog.tsx'
import type { IconEditorTarget } from './ServiceIcon.tsx'
import './App.css'
import { AppShell } from './AppShell.tsx'
import { ProjectGroup } from './ProjectGroup.tsx'
import { UpdateDialog } from './UpdateDialog.tsx'
import { DockerActivity } from './DockerActivity.tsx'
export { ProjectGroup, UpdateDialog, DockerActivity }

const errorMessage = (cause: unknown) => cause instanceof Error ? cause.message : 'Request failed'
const timeLabel = (value?: string | null) => value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : 'Not yet checked'

function usePolling<T>(url: string, interval: number, paused = false, onReceived?: (next: T) => void) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [revision, setRevision] = useState(0)
  const refresh = useCallback(() => setRevision(value => value + 1), [])
  const replace = useCallback((next: T) => { setData(next); setError('') }, [])
  useEffect(() => {
    if (paused) return
    let active = true
    let pending = false
    let controller: AbortController | null = null
    const poll = async () => {
      if (pending) return
      pending = true
      controller = new AbortController()
      const timeout = window.setTimeout(() => controller?.abort(), 12_000)
      try {
        const response = await fetch(url, { cache: 'no-store', signal: controller.signal })
        if (!response.ok) throw new Error((await response.text()).trim() || `Request failed (${response.status})`)
        const next = await response.json() as T
        if (active) { setData(next); setError(''); onReceived?.(next) }
      } catch (cause) {
        if (active) setError(cause instanceof DOMException && cause.name === 'AbortError' ? 'Request timed out' : errorMessage(cause))
      } finally {
        window.clearTimeout(timeout)
        pending = false
        if (active) setLoading(false)
      }
    }
    void poll()
    const timer = window.setInterval(poll, interval)
    return () => { active = false; window.clearInterval(timer); controller?.abort() }
  }, [url, interval, revision, paused, onReceived])
  return { data, error, loading, refresh, replace }
}

type AppProps = { user?: { username: string }; onAccounts?: () => void; onPublishers?: () => void; onLogout?: () => void; sessionError?: string }
export default function App({ user, onAccounts, onPublishers, onLogout, sessionError }: AppProps = {}) {
  const [iconTarget, setIconTarget] = useState<IconEditorTarget | null>(null)
  // Pause only icon polling during editing so an older response cannot overwrite a verified mutation.
  const iconsRequest = usePolling<IconList>('/api/icons', 30_000, iconTarget !== null)
  const iconsAvailable = iconsRequest.data?.available === true && !iconsRequest.error
  const projectsRequest = usePolling<{ projects: Project[] }>('/api/projects', 30_000)
  const monitorRequest = usePolling<Monitor>('/api/monitor', 15_000)
  const [acceptedOperations, setAcceptedOperations] = useState<Operation[]>([])
  const receivedOperations = useCallback((next: { operations: Operation[] }) => {
    setAcceptedOperations(current => current.filter(accepted => !next.operations.some(operation => operation.id === accepted.id)))
  }, [])
  const operationsRequest = usePolling<{ operations: Operation[] }>('/api/operations', 2_000, false, receivedOperations)
  const polledOperations = operationsRequest.data?.operations
  const operations = [...(polledOperations ?? []), ...acceptedOperations.filter(accepted => !polledOperations?.some(operation => operation.id === accepted.id))]
  const engineBusy = operations.some(operation => operation.state === 'running' || operation.state === 'queued')
  const finishedOperations = operations.filter(operation => operation.state === 'complete' || operation.state === 'error').map(operation => operation.id).join(',')
  const refreshProjects = projectsRequest.refresh
  const refreshMonitor = monitorRequest.refresh
  useEffect(() => { if (finishedOperations) { refreshProjects(); refreshMonitor() } }, [finishedOperations, refreshProjects, refreshMonitor])
  const [now, setNow] = useState(() => Date.now())
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const [checking, setChecking] = useState(false)
  const [updateTarget, setUpdateTarget] = useState<Project | null>(null)
  const checkLock = useRef(false)
  const [notice, setNotice] = useState<{ text: string; error: boolean } | null>(null)
  const projects = projectsWithOperations(projectsRequest.data?.projects ?? [], operations)
  const monitor = monitorRequest.data
  const error = monitorRequest.error || projectsRequest.error
  const connection = connectionState(monitor, error, now)
  const connected = connection === 'connected'
  const attention = projects.filter(needsAttention).length
  const updates = projects.reduce((total, project) => total + availableServices(project).length, 0)
  const visible = selectProjects(projects, query, filter)
  const incidents = monitor?.incidents ?? []
  const events = monitor?.events ?? []
  const refresh = () => { projectsRequest.refresh(); monitorRequest.refresh(); operationsRequest.refresh(); iconsRequest.refresh() }
  useEffect(() => { const timer = window.setInterval(() => setNow(Date.now()), 5_000); return () => window.clearInterval(timer) }, [])

  const check = async () => {
    if (checkLock.current) return
    checkLock.current = true
    setChecking(true); setNotice(null)
    try { await checkForUpdates(); setNotice({ text: 'Image check complete.', error: false }); projectsRequest.refresh() }
    catch (cause) { setNotice({ text: errorMessage(cause), error: true }) }
    finally { checkLock.current = false; setChecking(false) }
  }

  const headline = !connected ? connection === 'connecting' ? 'Connecting…' : 'Monitoring interrupted' : projectsRequest.loading ? 'Reading service health…' : attention || incidents.length ? 'Needs attention' : projects.length ? 'No problems detected' : 'No services'

  return <AppShell active="monitor" user={user} onAccounts={onAccounts} onPublishers={onPublishers} onLogout={onLogout} sessionError={sessionError} connection={<span className={`connection ${connected ? 'good' : 'muted'}`}><i />{connection === 'connected' ? 'Engine connected' : connection === 'connecting' ? 'Connecting' : connection === 'stale' ? 'Data stale' : 'Engine disconnected'}</span>}>
    <main className="workspace">
      <section className="page-heading"><div><h1>Monitor</h1><p className="page-description">Health, containers, and image updates in one place.</p></div><div className="heading-actions"><button className="button secondary" onClick={refresh} type="button">↻ <span>Refresh</span></button><button className="button primary" onClick={() => void check()} disabled={checking || !connected} type="button">{checking ? 'Checking images…' : 'Check for updates'}</button></div></section>
      {notice && <p role={notice.error ? 'alert' : 'status'} className={`notice ${notice.error ? 'danger' : ''}`}>{notice.text}</p>}
      <section className={`health-banner ${!connected ? 'neutral' : attention || incidents.length ? 'warning' : 'good'}`} aria-label="Monitoring status"><span className="health-symbol" aria-hidden="true">{!connected ? '◌' : attention || incidents.length ? '!' : '✓'}</span><div className="health-copy"><h2>{headline}</h2><p>{!connected ? (error || monitor?.error || (connection === 'stale' ? 'Stale data shown below.' : 'Waiting for a health check.')) : `${projects.length} groups · ${projects.reduce((count, project) => count + project.containers.length, 0)} containers${attention ? ` · ${attention} groups need attention` : ''}`}</p></div><div className="health-meta"><span>Checked</span><time>{timeLabel(monitor?.lastChecked)}</time></div></section>
      <details className="monitor-details"><summary>Monitor details</summary><div className="monitor-meta"><span><i className={`tiny-dot ${connected ? 'good' : ''}`} />Health polls every 15s <span className="meta-divider">/</span> Services every 30s</span><span>{monitor ? monitor.notificationConfigured ? 'Notifications configured' : 'Notifications not configured' : 'Checking notification status…'}</span></div></details>
      <DockerActivity operations={operations} loading={operationsRequest.loading} error={operationsRequest.error} onAcknowledged={operationsRequest.refresh} />
      {incidents.length > 0 && <section className="incidents" aria-labelledby="incident-heading"><div className="section-title"><h2 id="incident-heading">Needs attention <span className="count">{incidents.length}</span></h2><span className="section-note">{connected ? 'Active incidents' : 'Last observed incidents'}</span></div><div className="incident-list">{incidents.map(incident => <article className="incident" key={incident.id}><span className="incident-mark" aria-hidden="true">!</span><div><h3>{incident.project} <span>/</span> {incident.service || incident.name}</h3><p>{incident.reason || incident.status}</p></div><time title={incident.since}>Since {timeLabel(incident.since)}</time></article>)}</div></section>}
      <section id="services" aria-labelledby="services-title"><div className="section-title services-title"><h2 id="services-title">Services <span className="count">{projectsRequest.data ? projects.length : '—'}</span></h2></div><div className="toolbar"><div className="filters" aria-label="Filter service groups">{([['all', 'All', projects.length], ['attention', 'Needs attention', attention], ['updates', 'Updates', updates]] as const).map(([value, label, count]) => <button type="button" key={value} aria-pressed={filter === value} className={filter === value ? 'selected' : ''} onClick={() => setFilter(value)}>{label}<span>{projectsRequest.data ? count : '—'}</span></button>)}</div><label className="search"><svg viewBox="0 0 20 20" fill="none" aria-hidden="true"><circle cx="8.5" cy="8.5" r="5.5" stroke="currentColor" strokeWidth="1.5"/><path d="m13 13 4 4" stroke="currentColor" strokeWidth="1.5"/></svg><span className="sr-only">Search services</span><input type="search" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search services…" /></label></div>
        {projectsRequest.loading ? <div className="empty-state" role="status"><span className="loading-mark" /> <h3>Loading services</h3></div> : !projectsRequest.data && projectsRequest.error ? <div className="empty-state"><h3>Services couldn’t be loaded</h3><p>{projectsRequest.error}</p><button className="button secondary" onClick={projectsRequest.refresh}>Try again</button></div> : visible.length === 0 ? <div className="empty-state"><span className="empty-mark" aria-hidden="true">⌕</span><h3>{projects.length ? 'No matching services' : 'No containers discovered'}</h3><p>{projects.length ? 'Try a different search or switch to All.' : 'Containers will appear here when your engine reports them.'}</p>{projects.length > 0 && <button className="button secondary" onClick={() => { setQuery(''); setFilter('all') }}>Clear filters</button>}</div> : <div className="project-list">{visible.map(project => <ProjectGroup key={project.name} project={project} onReview={setUpdateTarget} icons={iconsRequest.data?.icons} iconsAvailable={iconsAvailable} onEditIcon={setIconTarget} actionsDisabled={!connected || checking || updateTarget !== null} />)}</div>}
      </section>
      <details className="activity"><summary className="activity-summary">Recent activity <span className="count">{events.length}</span></summary>{events.length === 0 ? <div className="activity-empty">{monitorRequest.loading ? 'Waiting for monitor events…' : monitorRequest.error ? 'Activity unavailable until monitoring reconnects.' : 'No events yet.'}</div> : <ol className="event-list">{recentEvents(events).map(event => <li key={event.id}><span className={`event-dot ${event.kind}`} /><div><p><strong>{event.project || event.name || 'Engine'}</strong><span className="event-kind">{event.kind.replaceAll('_', ' ')}</span></p><p className="event-message">{event.message}</p>{event.name && event.project && <span className="event-name">{event.name}</span>}</div><time title={event.time}>{timeLabel(event.time)}</time></li>)}</ol>}</details>
    </main>
    {iconTarget && <IconDialog target={iconTarget} available={iconsAvailable} onClose={() => setIconTarget(null)} onSaved={(next, reset) => { iconsRequest.replace(next); setNotice({ text: reset ? 'Default icon restored.' : 'Icon saved locally.', error: false }) }} />}
    {updateTarget && <UpdateDialog key={updateTarget.name} project={updateTarget} onClose={() => setUpdateTarget(null)} onUpdated={operation => { if (operation) setAcceptedOperations(current => [...current.filter(item => item.id !== operation.id), operation]); refresh() }} engineBusy={engineBusy} />}
  </AppShell>
}
