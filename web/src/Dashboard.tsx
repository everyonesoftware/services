import { useEffect, useState } from 'react'
import './dashboard.css'

type Container = { id: string; name: string; project: string; service: string; state: string; health?: string }
type Project = { name: string; status: string; containers: Container[] }
type VersionPayload = { version: number }
type Payload = { projects: Project[] }

export default function Dashboard() {
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
        if (active) { setProjects(data.projects ?? []); setError(''); setLastChecked(new Date()); setLoading(false) }
      } catch (cause) {
        if (active) { setError(cause instanceof Error ? cause.message : 'Could not load Compose projects'); setLoading(false) }
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
  const attention = projects.filter((project) => project.status === 'starting' || project.status === 'running-unverified').length
  return <main className="shell">
    <aside className="sidebar"><div className="brand"><span className="brand-mark">S</span><span>Services</span></div><div className="nav-label">WORKSPACE</div><div className="nav-item"><span className="nav-icon">▦</span> Compose projects <span className="nav-count">{projects.length}</span></div><div className="sidebar-bottom"><span className={`connection-dot ${error ? 'offline' : ''}`} /> Docker Engine <span className="connection-state">{error ? 'offline' : 'connected'}</span></div></aside>
    <section className="main-content"><header className="topbar"><div className="breadcrumb">Workspace <span>/</span> Compose projects</div><div className="topbar-right"><span className="live-indicator"><i /> LIVE</span><span className="version-badge">Version {version ?? '—'}</span><span className="avatar">S</span></div></header>
      <div className="content-wrap"><div className="page-heading"><div><div className="eyebrow">DOCKER COMPOSE</div><h1>Projects</h1><p className="subtitle">A live overview of your Compose services.</p></div><div className="refresh-note">↻ &nbsp;Refreshes every 30 seconds</div></div>
        {error && <div className="alert"><span className="alert-icon">!</span><div><strong>Unable to reach Docker</strong><p>{error}. Check the Docker socket mount and restart Services.</p></div></div>}
        <div className="stats-grid"><SummaryCard label="COMPOSE PROJECTS" value={loading ? '—' : String(projects.length)} icon="▦" tone="blue" detail="Discovered from Docker labels" /><SummaryCard label="UNHEALTHY" value={loading ? '—' : String(unhealthy)} icon="!" tone={unhealthy ? 'red' : 'green'} detail={unhealthy ? 'Needs attention' : 'No failing projects'} /><SummaryCard label="STARTING / UNVERIFIED" value={loading ? '—' : String(attention)} icon="◷" tone="amber" detail="Starting or lacking health checks" /></div>
        <div className="section-head"><div><h2>All projects</h2><span className="section-count">{projects.length}</span></div><span className="updated">{lastChecked ? `Updated ${lastChecked.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' })}` : 'Waiting for Docker…'}</span></div>
        {loading ? <div className="empty-state"><span className="spinner" /><p>Connecting to Docker Engine…</p></div> : projects.length === 0 && !error ? <div className="empty-state"><div className="empty-icon">▦</div><h3>No Compose projects found</h3><p>Projects appear here when their containers are running or stopped in Docker.</p></div> : <div className="project-list">{projects.map((project) => <ProjectCard key={project.name} project={project} />)}</div>}
        <footer className="footer">Services <span>•</span> Docker Compose health monitor <span>•</span> {lastChecked ? 'Connected to local Engine' : 'Connecting'}</footer>
      </div>
    </section>
  </main>
}
function SummaryCard({ label, value, icon, tone, detail }: { label: string; value: string; icon: string; tone: string; detail: string }) { return <div className="summary-card"><div className={`summary-icon ${tone}`}>{icon}</div><div className="summary-label">{label}</div><div className="summary-value">{value}</div><div className="summary-detail">{detail}</div></div> }
function ProjectCard({ project }: { project: Project }) { const label = project.status === 'running-unverified' ? 'No health checks' : project.status; return <article className="project-card"><div className="project-top"><div className="project-identity"><span className="project-icon">▦</span><div><h3>{project.name}</h3><span className="service-count">{project.containers.length} {project.containers.length === 1 ? 'container' : 'containers'}</span></div></div><span className={`status-pill ${project.status}`}><i />{label}</span></div><div className="container-list">{project.containers.map((container) => <div className="container-row" key={container.id}><span className={`container-indicator ${container.state === 'running' && container.health !== 'unhealthy' ? 'ok' : 'bad'}`} /><span className="container-name">{container.service || container.name}</span><span className="container-image">{container.name}</span><span className={`container-state ${container.health || container.state}`}>{container.health || container.state}</span></div>)}</div></article> }
