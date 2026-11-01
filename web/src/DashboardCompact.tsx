import { useEffect, useRef, useState } from 'react'
import { iconForProject } from './brandIcons.js'
import { splitImageReference } from './imageReference.js'
import { countAvailableUpdates } from './updateMetrics.js'
import './dashboard-compact.css'

type Container = {
  id: string
  name: string
  project: string
  service: string
  image?: string
  state: string
  health?: string
  updateAvailable?: boolean
  currentDigest?: string
  proposedDigest?: string
}
type Project = { name: string; status: string; containers: Container[]; updateAvailable?: boolean }
type VersionPayload = { version: number }
type Payload = { projects: Project[] }

export default function DashboardCompact() {
  const [projects, setProjects] = useState<Project[]>([])
  const [version, setVersion] = useState<number | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [lastChecked, setLastChecked] = useState<Date | null>(null)
  const [updateTarget, setUpdateTarget] = useState<Container | null>(null)
  const [refreshKey, setRefreshKey] = useState(0)

  useEffect(() => {
    let active = true
    const refresh = async () => {
      try {
        const response = await fetch('/api/projects', { cache: 'no-store' })
        if (!response.ok) throw new Error(response.status === 503 ? 'Docker Engine is unavailable' : `Request failed (${response.status})`)
        const data = await response.json() as Payload
        if (active) {
          setProjects(data.projects ?? [])
          setError('')
          setLastChecked(new Date())
          setLoading(false)
        }
      } catch (cause) {
        if (active) {
          setError(cause instanceof Error ? cause.message : 'Could not load Compose projects')
          setLoading(false)
        }
      }
    }
    void refresh()
    const timer = window.setInterval(refresh, 30_000)
    return () => { active = false; window.clearInterval(timer) }
  }, [refreshKey])

  useEffect(() => {
    let active = true
    void fetch('/api/version', { cache: 'no-store' })
      .then((response) => {
        if (!response.ok) throw new Error('Version unavailable')
        return response.json() as Promise<VersionPayload>
      })
      .then((data) => { if (active) setVersion(data.version) })
      .catch(() => { if (active) setVersion(null) })
    return () => { active = false }
  }, [])

  const unhealthy = projects.filter((project) => project.status === 'unhealthy').length
  const attention = projects.filter((project) => project.status === 'starting').length
  const containerCount = projects.reduce((total, project) => total + project.containers.length, 0)
  const needsAttention = unhealthy + attention
  const availableUpdates = countAvailableUpdates(projects)

  return <main className="shell">
    <aside className="sidebar">
      <div className="brand"><span className="brand-mark">S</span><span>Services</span></div>
      <div className="nav-label">WORKSPACE</div>
      <div className="nav-item"><span className="nav-icon">▦</span>Compose projects<span className="nav-count">{projects.length}</span></div>
      <div className="sidebar-bottom"><span className={`connection-dot ${error ? 'offline' : ''}`} />Docker Engine<span className="connection-state">{error ? 'offline' : 'connected'}</span></div>
    </aside>

    <section className="main-content">
      <header className="topbar">
        <div className="breadcrumb">Workspace <span>/</span> Compose projects</div>
        <div className="topbar-right"><span className="live-indicator"><i />LIVE</span><span className="version-badge">v{version ?? '—'}</span></div>
      </header>

      <div className="content-wrap">
        <div className="page-heading">
          <div><div className="eyebrow">DOCKER COMPOSE</div><h1>Projects</h1></div>
          <span className="refresh-note">↻ &nbsp;Refreshes every 30 seconds</span>
        </div>

        {error && <div className="alert"><span className="alert-icon">!</span><div><strong>Unable to reach Docker</strong><p>{error}. Check the Docker socket mount and restart Services.</p></div></div>}

        <div className="summary-strip" aria-label="Project summary">
          <SummaryMetric label="PROJECTS" value={loading ? '—' : projects.length} />
          <SummaryMetric label="CONTAINERS" value={loading ? '—' : containerCount} />
          <SummaryMetric label="NEEDS ATTENTION" value={loading ? '—' : needsAttention} tone={needsAttention ? 'attention' : 'good'} />
          <SummaryMetric label="UPDATES" value={loading ? '—' : availableUpdates} tone={availableUpdates ? 'attention' : 'good'} />
        </div>

        <div className="section-head">
          <div><h2>Compose groups</h2><span className="section-count">{projects.length}</span></div>
          <span className="updated">{lastChecked ? `Updated ${lastChecked.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' })}` : 'Waiting for Docker…'}</span>
        </div>

        {loading ? <div className="empty-state"><span className="spinner" /><p>Connecting to Docker Engine…</p></div>
          : projects.length === 0 && !error ? <div className="empty-state"><div className="empty-icon">▦</div><h3>No Compose projects found</h3><p>Projects appear here when Docker has discovered Compose containers.</p></div>
            : <div className="project-list">{projects.map((project) => <ProjectCard key={project.name} project={project} onOpenUpdate={setUpdateTarget} />)}</div>}

        <footer className="footer">Services <span>•</span> Docker Compose health monitor <span>•</span> {lastChecked ? 'Connected to local Engine' : 'Connecting'}</footer>
      </div>
    </section>
    <UpdateDialog
      key={updateTarget?.id ?? 'none'}
      container={updateTarget}
      onClose={() => setUpdateTarget(null)}
      onUpdated={() => setRefreshKey((value) => value + 1)}
    />
  </main>
}

function SummaryMetric({ label, value, tone = '' }: { label: string; value: number | string; tone?: string }) {
  return <div className="summary-metric"><span className="summary-label">{label}</span><strong className={tone}>{value}</strong></div>
}

function ProjectCard({ project, onOpenUpdate }: { project: Project; onOpenUpdate: (container: Container) => void }) {
  const labels: Record<string, string> = { healthy: 'Healthy', running: 'Running', starting: 'Starting', unhealthy: 'Unhealthy' }
  const label = labels[project.status] ?? project.status
  const icon = iconForProject(project)
  const updateCount = project.containers.filter((container) => container.updateAvailable).length
  return <details className="project-card">
    <summary className="project-top">
      <div className="project-identity">
        <span className="project-icon" title={icon ? `${icon.title} logo, identified from a running image` : 'Compose project'} aria-label={icon ? `${icon.title} logo` : 'Compose project icon'}>
          {icon ? <svg viewBox="0 0 24 24" aria-hidden="true"><path fill={`#${icon.hex}`} d={icon.path} /></svg> : <span aria-hidden="true">▦</span>}
        </span>
        <div><h3>{project.name}</h3><span className="service-count">{project.containers.length} {project.containers.length === 1 ? 'container' : 'containers'}</span></div>
      </div>
      <div className="project-status-group">
        {updateCount > 0 && <span className="project-update-count" title="Use a container's update button to review that service">{updateCount} update{updateCount === 1 ? '' : 's'}</span>}
        <span className={`status-pill ${project.status}`}><i />{label}</span>
      </div>
    </summary>
    <div className="container-list">
      {project.containers.map((container) => {
        const status = container.health || container.state
        const tone = status === 'healthy' || status === 'running' ? 'ok' : status === 'starting' || status === 'created' ? 'pending' : 'bad'
        const image = splitImageReference(container.image || '')
        return <div className="container-row" key={container.id}>
          <span className={`container-indicator ${tone}`} />
          <div className="container-name-cell">
            <span className="container-name" title={container.name}>{container.name || container.service || 'Unnamed container'}</span>
            {container.service && container.service !== container.name && <span className="container-service">{container.service}</span>}
          </div>
          <span className="container-image" title={container.image || 'Image not reported'}>{image.image}</span>
          <span className="container-tag" title={image.tag}>{image.tag}</span>
          <span className={`container-state ${status}`}>
            {container.updateAvailable && <button
              type="button"
              className="update-pill container-update-pill"
              aria-label={`Review update for ${container.name || container.service}`}
              onClick={() => onOpenUpdate(container)}
            >Update available</button>}
            <span>{container.state}</span>
            {container.health && <span className="container-health">{container.health}</span>}
          </span>
        </div>
      })}
    </div>
  </details>
}

function UpdateDialog({
  container,
  onClose,
  onUpdated,
}: {
  container: Container | null
  onClose: () => void
  onUpdated: () => void
}) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [busy, setBusy] = useState<'pull' | 'update' | null>(null)
  const [message, setMessage] = useState('')
  const [pulled, setPulled] = useState(false)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (container && !dialog.open) dialog.showModal()
    if (!container && dialog.open) dialog.close()
  }, [container])

  const dismiss = () => {
    if (!busy) onClose()
  }

  const runAction = async (action: 'pull' | 'update') => {
    if (!container || busy) return
    let updated = false
    setBusy(action)
    setMessage('')
    try {
      const response = await fetch(`/api/containers/${encodeURIComponent(container.id)}/${action}`, { method: 'POST' })
      if (!response.ok) {
        const detail = (await response.text()).trim()
        throw new Error(detail || `Request failed (${response.status})`)
      }
      if (action === 'pull') {
        setPulled(true)
        setMessage('Image pulled. The running container is unchanged. The digest above is from the last registry check; Update will pull the current tag again before recreating the service.')
      } else {
        updated = true
      }
    } catch (cause) {
      setMessage(cause instanceof Error ? cause.message : 'Could not complete the Compose operation.')
    } finally {
      setBusy(null)
    }
    if (updated) {
      onUpdated()
      onClose()
    }
  }

  const image = splitImageReference(container?.image || '')
  const currentState = container ? `${container.state}${container.health ? ` · ${container.health}` : ''}` : ''

  return <dialog
    ref={dialogRef}
    className="update-dialog"
    aria-labelledby="update-dialog-title"
    onCancel={(event) => { event.preventDefault(); dismiss() }}
    onClick={(event) => { if (event.target === event.currentTarget) dismiss() }}
  >
    {container && <div className="update-dialog-content">
      <div className="update-dialog-heading">
        <div><span className="eyebrow">IMAGE UPDATE</span><h2 id="update-dialog-title">Review update</h2></div>
        <p>{container.name || container.service}</p>
      </div>
      <p className="update-dialog-copy">Pull downloads the image only. Update pulls the current tag again and recreates every replica of this Compose service using the current Compose files and .env values, without rebuilding or starting dependencies. Digests show the last registry check; mutable tags may move before Pull.</p>
      <div className="update-comparison">
        <section className="update-state-card current">
          <span className="update-state-heading">CURRENT</span>
          <div className="update-state-detail"><span>Image</span><code title={container.image}>{image.image}</code></div>
          <div className="update-state-detail"><span>Tag</span><code title={image.tag}>{image.tag}</code></div>
          <div className="update-state-detail"><span>Digest</span><code title={container.currentDigest}>{container.currentDigest || 'Not reported'}</code></div>
          <div className="update-state-status"><span>State</span><strong>{currentState}</strong></div>
        </section>
        <span className="update-comparison-arrow" aria-hidden="true">→</span>
        <section className="update-state-card proposed">
          <span className="update-state-heading">PROPOSED</span>
          <div className="update-state-detail"><span>Image</span><code title={container.image}>{image.image}</code></div>
          <div className="update-state-detail"><span>Tag</span><code title={image.tag}>{image.tag}</code></div>
          <div className="update-state-detail"><span>Digest</span><code title={container.proposedDigest}>{container.proposedDigest || 'New image'}</code></div>
          <div className="update-state-status"><span>State</span><strong>{pulled ? 'Pulled · not applied' : 'Will run after update'}</strong></div>
        </section>
      </div>
      {message && <p className={`update-dialog-message ${busy ? 'pending' : message.startsWith('Image pulled') ? 'success' : 'error'}`} role={message.startsWith('Image pulled') ? 'status' : 'alert'}>{message}</p>}
      <div className="update-dialog-actions">
        <button type="button" className="dialog-button secondary" onClick={dismiss} disabled={busy !== null}>Cancel</button>
        <button type="button" className="dialog-button secondary" onClick={() => void runAction('pull')} disabled={busy !== null}>
          {busy === 'pull' ? 'Pulling…' : 'Pull'}
        </button>
        <button type="button" className="dialog-button primary" onClick={() => void runAction('update')} disabled={busy !== null}>
          {busy === 'update' ? 'Updating…' : 'Update'}
        </button>
      </div>
    </div>}
  </dialog>
}
