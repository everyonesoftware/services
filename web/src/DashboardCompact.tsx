import { useEffect, useState } from 'react'
import { iconForProject } from './brandIcons.js'
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
  }, [])

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
            : <div className="project-list">{projects.map((project) => <ProjectCard key={project.name} project={project} />)}</div>}

        <footer className="footer">Services <span>•</span> Docker Compose health monitor <span>•</span> {lastChecked ? 'Connected to local Engine' : 'Connecting'}</footer>
      </div>
    </section>
  </main>
}

function SummaryMetric({ label, value, tone = '' }: { label: string; value: number | string; tone?: string }) {
  return <div className="summary-metric"><span className="summary-label">{label}</span><strong className={tone}>{value}</strong></div>
}

function ProjectCard({ project }: { project: Project }) {
  const labels: Record<string, string> = { healthy: 'Healthy', running: 'Running', starting: 'Starting', unhealthy: 'Unhealthy' }
  const label = labels[project.status] ?? project.status
  const icon = iconForProject(project)
  return <details className="project-card">
    <summary className="project-top">
      <div className="project-identity">
        <span className="project-icon" title={icon ? `${icon.title} logo, identified from a running image` : 'Compose project'} aria-label={icon ? `${icon.title} logo` : 'Compose project icon'}>
          {icon ? <svg viewBox="0 0 24 24" aria-hidden="true"><path fill={`#${icon.hex}`} d={icon.path} /></svg> : <span aria-hidden="true">▦</span>}
        </span>
        <div><h3>{project.name}</h3><span className="service-count">{project.containers.length} {project.containers.length === 1 ? 'container' : 'containers'}</span></div>
      </div>
      <div className="project-status-group">
        {project.updateAvailable && <span className="update-pill">Update available</span>}
        <span className={`status-pill ${project.status}`}><i />{label}</span>
      </div>
    </summary>
    <div className="container-list">
      {project.containers.map((container) => {
        const status = container.health || container.state
        const tone = status === 'healthy' || status === 'running' ? 'ok' : status === 'starting' || status === 'created' ? 'pending' : 'bad'
        return <div className="container-row" key={container.id}>
          <span className={`container-indicator ${tone}`} />
          <span className="container-name" title={container.name}>{container.service || container.name}</span>
          <span className="container-image" title={container.image || 'Image not reported'}>{container.image || 'Image not reported'}</span>
          <span className={`container-state ${status}`}>
            {container.updateAvailable && <span className="update-pill container-update-pill">Update available</span>}
            <span>{status}</span>
          </span>
        </div>
      })}
    </div>
  </details>
}
