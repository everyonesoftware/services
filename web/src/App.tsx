import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { checkForUpdates, undoResolution } from './api.ts'
import { availableServices, canUpdateProject, connectionState, isSnoozed, needsAttention, projectsWithOperations, selectProjects } from './monitor.ts'
import type { Container, Filter, IconList, Incident, Monitor, Operation, Project } from './types.ts'
import { resolutionEntries } from './resolution.ts'
import { ResolutionDialog } from './ResolutionDialog.tsx'
import type { ResolutionTarget } from './ResolutionDialog.tsx'
import { IconDialog } from './IconDialog.tsx'
import type { IconEditorTarget } from './ServiceIcon.tsx'
import './App.css'
import './ResolutionDialog.css'
import { AppShell } from './AppShell.tsx'
import { ProjectGroup } from './ProjectGroup.tsx'
import { ContainerDetail, ServiceDetail } from './ServiceDetails.tsx'
import { Breadcrumb } from './Breadcrumb.tsx'
import { serviceHref, serviceRoute } from './navigation.ts'
import { UpdateDialog } from './UpdateDialog.tsx'
import { DockerActivity } from './DockerActivity.tsx'
import { ActivityFeed } from './ActivityFeed.tsx'
import { PageHeader } from './PageHeader.tsx'
import { FilterSearch, FilterToolbar, IconDropdown, ResetView } from './FilterToolbar.tsx'
import type { PublisherStatus } from './publisherApi.ts'
import { publisherHref, publisherNeedsAttention, publishersForContainers, publisherSignal } from './publisherReconciliation.ts'
export { ProjectGroup, ServiceDetail, ContainerDetail, UpdateDialog, DockerActivity }

const errorMessage = (cause: unknown) => cause instanceof Error ? cause.message : 'Request failed'
const timeLabel = (value?: string | null) => value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : 'Not yet checked'

function usePolling<T>(url: string, interval: number, paused = false, onReceived?: (next: T) => void, refreshKey = '') {
  const [snapshot, setSnapshot] = useState<{ data: T | null; key: string | null }>({ data: null, key: null })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [revision, setRevision] = useState(0)
  const generation = useRef(0)
  const invalidate = useCallback(() => { generation.current++ }, [])
  const refresh = useCallback(() => { generation.current++; setRevision(value => value + 1) }, [])
  const replace = useCallback((next: T) => { setSnapshot(current => ({ ...current, data: next })); setError('') }, [])
  useEffect(() => {
    if (paused) return
    let active = true
    let pending = false
    let controller: AbortController | null = null
    const poll = async () => {
      if (pending) return
      pending = true
      const requestGeneration = generation.current
      controller = new AbortController()
      const timeout = window.setTimeout(() => controller?.abort(), 12_000)
      try {
        const response = await fetch(url, { cache: 'no-store', signal: controller.signal })
        if (!response.ok) throw new Error((await response.text()).trim() || `Request failed (${response.status})`)
        const next = await response.json() as T
        // Only the key captured when this request started can be reconciled by it.
        if (active && requestGeneration === generation.current) { setSnapshot({ data: next, key: refreshKey }); setError(''); onReceived?.(next) }
      } catch (cause) {
        if (active && requestGeneration === generation.current) setError(cause instanceof DOMException && cause.name === 'AbortError' ? 'Request timed out' : errorMessage(cause))
      } finally {
        window.clearTimeout(timeout)
        pending = false
        if (active) setLoading(false)
      }
    }
    void poll()
    const timer = window.setInterval(poll, interval)
    return () => { active = false; window.clearInterval(timer); controller?.abort() }
  }, [url, interval, revision, paused, onReceived, refreshKey])
  return { data: snapshot.data, dataKey: snapshot.key, refreshing: snapshot.key !== refreshKey, error, loading, refresh, replace, invalidate }
}

type AppProps = { activePage?: 'monitor' | 'activity'; user?: { username: string }; onAccounts?: () => void; onPublishers?: () => void }
export default function App({ activePage = 'monitor', user, onAccounts, onPublishers }: AppProps = {}) {
  const [iconTarget, setIconTarget] = useState<IconEditorTarget | null>(null)
  const [resolutionTarget, setResolutionTarget] = useState<ResolutionTarget | null>(null)
  const [resolutionPending, setResolutionPending] = useState(false)
  const [undoToken, setUndoToken] = useState('')
  const [undoPending, setUndoPending] = useState(false)
  const undoLock = useRef(false)
  // Pause only icon polling during editing so an older response cannot overwrite a verified mutation.
  const iconsRequest = usePolling<IconList>('/api/icons', 30_000, iconTarget !== null)
  const iconsAvailable = iconsRequest.data?.available === true && !iconsRequest.error

  const monitorRequest = usePolling<Monitor>('/api/monitor', 15_000, resolutionPending || undoPending)
  const publishersRequest = usePolling<PublisherStatus>('/api/publishers', 15_000)
  const publishers = publishersRequest.data?.connections ?? []
  const publisherAttention = publishers.filter(publisherNeedsAttention)
  const [acceptedOperations, setAcceptedOperations] = useState<Operation[]>([])
  const receivedOperations = useCallback((next: { operations: Operation[] }) => {
    setAcceptedOperations(current => current.filter(accepted => !next.operations.some(operation => operation.id === accepted.id)))
  }, [setAcceptedOperations])
  const operationsRequest = usePolling<{ operations: Operation[] }>('/api/operations', 2_000, false, receivedOperations)
  const polledOperations = operationsRequest.data?.operations
  const operations = [...(polledOperations ?? []), ...acceptedOperations.filter(accepted => !polledOperations?.some(operation => operation.id === accepted.id))]
  const engineBusy = operations.some(operation => operation.state === 'running' || operation.state === 'queued')
  const finishedOperations = operations.filter(operation => operation.state === 'complete' || operation.state === 'error').map(operation => operation.id).sort().join(',')
  const projectsRequest = usePolling<{ projects: Project[] }>('/api/projects', 30_000, resolutionPending || undoPending, undefined, finishedOperations)
  const refreshMonitor = monitorRequest.refresh
  useEffect(() => { if (finishedOperations) refreshMonitor() }, [finishedOperations, refreshMonitor])
  const [now, setNow] = useState(() => Date.now())
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const search = useRef<HTMLInputElement>(null)
  const [checking, setChecking] = useState(false)
  // A fresh wrapper identifies each review, including reopening the same project.
  const [updateTarget, setUpdateTarget] = useState<{ project: Project } | null>(null)
  const checkLock = useRef(false)
  const [notice, setNotice] = useState<{ text: string; error: boolean } | null>(null)
  const [hash, setHash] = useState(() => typeof window === 'undefined' ? '' : window.location.hash)
  const scrollPositions = useRef(new Map<string, number>())
  const route = serviceRoute(hash)
  useEffect(() => {
    let currentKey = window.location.hash || '#monitor'
    const changed = () => {
      // Capture before React swaps a long list for a shorter page and clamps its scroll.
      scrollPositions.current.set(currentKey, document.getElementById('page-content')?.scrollTop ?? 0)
      currentKey = window.location.hash || '#monitor'
      setHash(window.location.hash); setUpdateTarget(null); setIconTarget(null)
    }
    window.addEventListener('hashchange', changed)
    return () => window.removeEventListener('hashchange', changed)
  }, [])
  useLayoutEffect(() => {
    const content = document.getElementById('page-content')
    if (!content) return
    const positions = scrollPositions.current
    const key = hash || '#monitor'
    content.scrollTop = positions.get(key) ?? 0
    const heading = content.querySelector('h1')
    if (heading) { heading.tabIndex = -1; heading.focus({ preventScroll: true }) }
  }, [hash, activePage])
  const projects = projectsWithOperations(projectsRequest.data?.projects ?? [], operations, projectsRequest.refreshing)
  const currentUpdateProject = updateTarget && projects.find(project => project.name === updateTarget.project.name)
  // Invalidate before committing stale review content, not in a later effect.
  // Never replace the reviewed snapshot: a newer image requires a new review.
  if (updateTarget && (!currentUpdateProject || !canUpdateProject(currentUpdateProject))) setUpdateTarget(null)
  const monitor = monitorRequest.data
  const eventConnection = connectionState(monitor, monitorRequest.error, now)
  const eventsError = monitorRequest.error || monitor?.error || (monitor?.connected === false ? 'Engine disconnected' : eventConnection === 'stale' ? 'Monitoring data is stale' : '')
  const error = monitorRequest.error || projectsRequest.error
  const connection = connectionState(monitor, error, now)
  const connected = connection === 'connected'
  const hasPublisherAttention = (project: Project) => publishersForContainers(publisherAttention, project.containers).length > 0
  const attention = projects.filter(project => needsAttention(project) || hasPublisherAttention(project)).length
  const updates = projects.reduce((total, project) => total + availableServices(project).length, 0)
  const visible = selectProjects(projects, query, filter === 'attention' ? 'all' : filter).filter(project => filter !== 'attention' || needsAttention(project) || hasPublisherAttention(project))
  const incidents = monitor?.incidents ?? []
  const activeIncidents = incidents.filter(incident => incident.status !== 'missing' || !isSnoozed(incident.snoozedUntil, now))
  const missingIncidents = incidents.filter(incident => incident.status === 'missing')
  const snoozedMissing = missingIncidents.filter(incident => isSnoozed(incident.snoozedUntil, now))
  const events = monitor?.events ?? []
  const refresh = () => { projectsRequest.refresh(); monitorRequest.refresh(); operationsRequest.refresh(); iconsRequest.refresh(); publishersRequest.refresh() }
  useEffect(() => { const timer = window.setInterval(() => setNow(Date.now()), 5_000); return () => window.clearInterval(timer) }, [])

  const resolve = (targets: Incident[], trigger: HTMLElement | null) => {
    if (!connected || undoPending || resolutionPending) return
    setResolutionTarget({ entries: resolutionEntries(targets, projects, events), trigger })
  }
  const resolutionBusy = (pending: boolean) => {
    if (pending) { projectsRequest.invalidate(); monitorRequest.invalidate() }
    setResolutionPending(pending)
  }
  const undo = async () => {
    if (undoLock.current || !undoToken || !connected) return
    undoLock.current = true
    projectsRequest.invalidate(); monitorRequest.invalidate()
    setUndoPending(true)
    try {
      await undoResolution(undoToken)
      setUndoToken('')
      setNotice({ text: 'Resolution undone. Monitoring refreshed.', error: false })
      projectsRequest.refresh(); monitorRequest.refresh()
    } catch (cause) { setNotice({ text: errorMessage(cause), error: true }) }
    finally { undoLock.current = false; setUndoPending(false) }
  }

  const check = async () => {
    if (checkLock.current) return
    checkLock.current = true
    setChecking(true); setNotice(null)
    try { await checkForUpdates(); setNotice({ text: 'Image check complete.', error: false }); projectsRequest.refresh() }
    catch (cause) { setNotice({ text: errorMessage(cause), error: true }) }
    finally { checkLock.current = false; setChecking(false) }
  }

  const feedback = <>
    {notice && <div role={notice.error ? 'alert' : 'status'} className={`notice resolution-notice ${notice.error ? 'danger' : ''}`}><span>{notice.text}</span>{undoToken && <button type="button" className="button secondary" disabled={undoPending || resolutionPending || !connected} onClick={() => void undo()}>{undoPending ? 'Undoing…' : 'Undo'}</button>}</div>}
    {!connected && <p className={`notice ${connection === 'connecting' ? '' : 'danger'}`} role={connection === 'connecting' ? 'status' : 'alert'}>{connection === 'connecting' ? 'Connecting…' : `Monitoring interrupted: ${error || monitor?.error || (connection === 'stale' ? 'Monitoring data is stale.' : 'Engine disconnected.')}. Last observed data is shown; current state cannot be confirmed.`}</p>}
  </>
  const detailProps = (project: Project) => ({
    publishers: publishersForContainers(publishers, project.containers),
    project, onReview: (reviewed: Project) => setUpdateTarget({ project: reviewed }), icons: iconsRequest.data?.icons, iconsAvailable, onEditIcon: setIconTarget,
    onResolve: (container: Container, trigger: HTMLElement) => { const incident = missingIncidents.find(item => item.project === project.name && item.name === container.name && item.service === (container.service || '')); if (incident) resolve([incident], trigger) },
    resolveDisabled: !connected || undoPending || resolutionPending,
    actionsDisabled: !connected || checking || updateTarget !== null || resolutionTarget !== null || undoPending,
    events, eventsLoading: monitorRequest.loading, eventsError, feedback,
  })
  const detailFallback = (title: string, explanation: string, retry = false) => <main className="workspace service-detail"><Breadcrumb parentProject={route.kind === 'service' && route.container !== undefined ? route.project : undefined} /><PageHeader title={title} />{feedback}<div className="empty-state"><p>{explanation}</p>{retry && <button className="button secondary" onClick={projectsRequest.refresh}>Try again</button>}<a className="button secondary" href={route.kind === 'service' && route.container ? serviceHref(route.project) : '#monitor'}>{route.kind === 'service' && route.container ? 'Back to service' : 'Back to Monitor'}</a></div></main>
  const renderDetail = () => {
    if (route.kind === 'invalid') return detailFallback('Page not found', 'This service link is not valid. Return to Monitor to choose a service.')
    if (route.kind !== 'service') return null
    if (projectsRequest.loading) return detailFallback('Loading services', 'Retrieving the latest inventory…')
    if (!projectsRequest.data) return detailFallback('Services couldn’t be loaded', projectsRequest.error || 'Inventory is unavailable.', true)
    const project = projects.find(item => item.name === route.project)
    if (!project) return projectsRequest.error ? detailFallback('Services couldn’t be loaded', projectsRequest.error, true) : detailFallback('Service not found', 'This service is not in the current inventory. It may have been removed or renamed.')
    if (route.container === undefined) return <ServiceDetail {...detailProps(project)} />
    const container = project.containers.find(item => item.name === route.container)
    if (!container) return projectsRequest.error ? detailFallback('Services couldn’t be loaded', projectsRequest.error, true) : detailFallback('Container not found', 'This container is not in this service’s current inventory. Return to the service to choose a container.')
    return <ContainerDetail {...detailProps(project)} container={container} />
  }

  return <AppShell publisherAttention={publisherAttention.length} active={activePage} user={user} onAccounts={onAccounts} onPublishers={onPublishers} connection={<span className={`connection ${connected ? 'good' : 'muted'}`}><i />{connection === 'connected' ? 'Engine connected' : connection === 'connecting' ? 'Connecting' : connection === 'stale' ? 'Data stale' : 'Engine disconnected'}</span>}>
    {activePage === 'activity' ? <ActivityFeed operations={operations} events={events} operationsLoading={operationsRequest.loading} eventsLoading={monitorRequest.loading} operationsError={operationsRequest.error} eventsError={eventsError} now={now} onAcknowledged={operationsRequest.refresh} /> : route.kind !== 'monitor' ? renderDetail() : <main className="workspace">
      <PageHeader title="Services" id="services-title" actions={<><button className="button secondary monitor-header-action" onClick={refresh} type="button" aria-label="Refresh" title="Refresh"><span aria-hidden="true">↻</span><span className="monitor-action-label">Refresh</span></button><button className="button primary monitor-header-action" onClick={() => void check()} disabled={checking || !connected} type="button" aria-label={checking ? 'Checking images…' : 'Check for updates'} title={checking ? 'Checking images…' : 'Check for updates'}><svg className="monitor-check-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3v12m-4-4 4 4 4-4M4 16v5h16v-5" /></svg><span className="monitor-action-label">{checking ? 'Checking images…' : 'Check for updates'}</span></button></>} />
      {feedback}

      {publisherAttention.length > 0 && <section className="publisher-attention" aria-label="Publisher attention"><div className="section-title"><h2>Needs attention <span className="count">{publisherAttention.length}</span></h2><span className="section-note">Publisher connections · runtime health is separate</span></div>{publishersRequest.error && <p className="notice danger">Publisher status could not be refreshed. Last observed attention is shown.</p>}<div className="publisher-attention-list">{publisherAttention.map(publisher => <article className="publisher-attention-item" key={publisher.id}><div><strong>{projects.filter(project => publishersForContainers([publisher], project.containers).length).map(project => project.name).join(', ') || publisher.repository.fullName}</strong><p>{publisherSignal(publisher)}. Running containers are unaffected by publisher reconciliation.</p><code>{publisher.imageRepository}</code></div><a className="button primary" href={publisherHref(publisher.id)}>{publisher.reconciliation?.state === 'awaiting_publication' ? 'View publisher status' : 'Review reconciliation'}</a></article>)}</div></section>}

      {incidents.length > 0 && <section className="incidents" aria-labelledby="incident-heading"><div className="section-title"><h2 id="incident-heading">{activeIncidents.length ? 'Needs attention' : 'Missing · alerts snoozed'} <span className="count">{activeIncidents.length || snoozedMissing.length}</span></h2><span className="section-note">{connected ? 'Observed incidents' : 'Last observed incidents'}</span></div><div className="incident-list">{incidents.map(incident => <article className={`incident ${incident.status === 'missing' && isSnoozed(incident.snoozedUntil, now) ? 'snoozed-incident' : ''}`} key={incident.id}><span className="incident-mark" aria-hidden="true">{incident.status === 'missing' && isSnoozed(incident.snoozedUntil, now) ? '◌' : '!'}</span><div className="incident-copy"><h3>{incident.project} <span>/</span> {incident.service || incident.name}</h3><p>{incident.reason || incident.status}</p>{incident.status === 'missing' && isSnoozed(incident.snoozedUntil, now) && <p>Missing · alerts snoozed until {new Date(incident.snoozedUntil!).toLocaleString()}</p>}</div><time title={incident.since}>Since {timeLabel(incident.since)}</time>{incident.status === 'missing' && <button className="button secondary incident-resolve" type="button" disabled={!connected || undoPending || resolutionPending} onClick={event => resolve([incident], event.currentTarget)}>Resolve…</button>}</article>)}</div>{missingIncidents.length > 1 && <div className="resolution-batch"><span>Review and handle missing entries together.</span><button className="button secondary" type="button" disabled={!connected || undoPending || resolutionPending} onClick={event => resolve(missingIncidents, event.currentTarget)}>Resolve {missingIncidents.length === 2 ? 'both' : 'all'} missing entries…</button></div>}</section>}
      <section id="services" aria-labelledby="services-title">
        <FilterToolbar label="Monitor filters">
          <FilterSearch label="Search services" inputRef={search} value={query} onChange={setQuery} />
          <div className="activity-icon-group">
            <IconDropdown kind="filter" label="Service filter" value={filter} choices={[
              { value: 'all', label: `All services (${projectsRequest.data ? projects.length : '—'})` },
              { value: 'attention', label: `Needs attention (${projectsRequest.data ? attention : '—'})` },
              { value: 'updates', label: `Updates (${projectsRequest.data ? updates : '—'})` },
            ]} active={filter !== 'all'} onChange={value => setFilter(value as Filter)} />
            {(query || filter !== 'all') && <ResetView onClick={() => { setQuery(''); setFilter('all'); search.current?.focus({ preventScroll: true }) }} />}
          </div>
        </FilterToolbar>
        {projectsRequest.loading ? <div className="empty-state" role="status"><span className="loading-mark" /> <h3>Loading services</h3></div> : !projectsRequest.data && projectsRequest.error ? <div className="empty-state"><h3>Services couldn’t be loaded</h3><p>{projectsRequest.error}</p><button className="button secondary" onClick={projectsRequest.refresh}>Try again</button></div> : visible.length === 0 ? <div className="empty-state"><span className="empty-mark" aria-hidden="true">⌕</span><h3>{projects.length ? 'No matching services' : 'No containers discovered'}</h3><p>{projects.length ? 'Try a different search or switch to All.' : 'Containers will appear here when your engine reports them.'}</p>{projects.length > 0 && <button className="button secondary" onClick={() => { setQuery(''); setFilter('all') }}>Clear filters</button>}</div> : <div className="project-list">{visible.map(project => <ProjectGroup key={project.name} {...detailProps(project)} />)}</div>}
      </section>
    </main>}
    {resolutionTarget && <ResolutionDialog target={resolutionTarget} connected={connected} onClose={() => setResolutionTarget(null)} onPending={resolutionBusy} onResolved={token => { setUndoToken(token); setNotice({ text: 'Resolution applied. Docker resources unchanged.', error: false }); projectsRequest.refresh(); monitorRequest.refresh() }} />}
    {iconTarget && <IconDialog target={iconTarget} available={iconsAvailable} onClose={() => setIconTarget(null)} onSaved={(next, reset) => { iconsRequest.replace(next); setNotice({ text: reset ? 'Default icon restored.' : 'Icon saved locally.', error: false }) }} />}
    {updateTarget && <UpdateDialog key={updateTarget.project.name} project={updateTarget.project} onClose={() => setUpdateTarget(current => current === updateTarget ? null : current)} onUpdated={operation => { if (operation) setAcceptedOperations(current => [...current.filter(item => item.id !== operation.id), operation]); refresh() }} engineBusy={engineBusy} />}
  </AppShell>
}
