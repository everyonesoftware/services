import type { Container, Filter, Monitor, Operation, Project } from './types.ts'

export function projectsWithOperations(projects: Project[], operations: Operation[], refreshing = false): Project[] {
  const active = new Map<string, 'queued' | 'running' | 'refreshing'>()
  if (refreshing) {
    for (const operation of operations) {
      if (operation.state === 'complete' || operation.state === 'error') active.set(operation.project, 'refreshing')
    }
  }
  for (const operation of operations) {
    if (operation.state === 'running' || (operation.state === 'queued' && active.get(operation.project) !== 'running')) active.set(operation.project, operation.state)
  }
  return projects.map(project => active.has(project.name) ? { ...project, updateState: active.get(project.name) } : project)
}

export function recentEvents<T extends { time: string }>(events: T[], limit = 20): T[] {
  return [...events].sort((a, b) => Date.parse(b.time) - Date.parse(a.time)).slice(0, limit)
}

export function connectionState(monitor: Pick<Monitor, 'connected' | 'lastChecked' | 'error'> | null, error: string, now: number): 'connecting' | 'connected' | 'stale' | 'disconnected' {
  if (!monitor) return error ? 'disconnected' : 'connecting'
  if (!monitor.connected) return 'disconnected'
  const checked = monitor.lastChecked ? Date.parse(monitor.lastChecked) : NaN
  if (error || monitor.error || !Number.isFinite(checked) || now - checked > 45_000) return 'stale'
  return 'connected'
}

export function selectProjects(projects: Project[], query: string, filter: Filter): Project[] {
  const needle = query.trim().toLowerCase()
  const rank = (project: Project) => needsAttention(project) ? 3 - statusSeverity(projectHealthStatus(project)) : availableServices(project).length ? 4 : 5
  return projects.filter(project => {
    const text = [project.name, ...project.containers.flatMap(c => [c.name, c.service, c.image, c.monitorReason])].join(' ').toLowerCase()
    return text.includes(needle) && (filter !== 'attention' || needsAttention(project)) && (filter !== 'updates' || availableServices(project).length > 0)
  }).sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name))
}

export type UpdateService = { service: string; image: string; currentDigests: string[]; proposedDigests: string[]; replicas: number }

export function availableServices(project: Project): UpdateService[] {
  if (project.updateState) return []
  const groups = new Map<string, Container[]>()
  const keyOf = (container: Container) => container.service || container.name || container.id
  const available = new Set(project.containers.filter(c => c.updateAvailable).map(keyOf))
  for (const container of project.containers.filter(c => available.has(keyOf(c)))) {
    const key = keyOf(container)
    groups.set(key, [...(groups.get(key) || []), container])
  }
  return [...groups].map(([service, containers]) => ({
    service,
    image: [...new Set(containers.map(c => c.image || 'Not reported'))].join(', '),
    currentDigests: [...new Set(containers.map(c => c.currentDigest || 'Not reported'))],
    proposedDigests: [...new Set(containers.filter(c => c.updateAvailable).map(c => c.proposedDigest || 'Not reported'))],
    replicas: project.containers.filter(c => (c.service || c.name || c.id) === service).length,
  }))
}

export function canUpdateProject(project: Project): boolean {
  const updates = project.containers.filter(c => c.updateAvailable)
  return !project.updateState && project.name !== 'Standalone' && !!project.name.trim() && updates.length > 0 && updates.every(c => !!c.service?.trim() && c.project === project.name)
}

export function containerStatus(container: Container): string {
  if (container.monitorStatus === 'running') return 'running-unverified'
  if (container.monitorStatus) return container.monitorStatus
  if (container.state !== 'running') return 'unhealthy'
  return container.health || 'running-unverified'
}

export function needsAttention(project: Project): boolean {
  if (!project.updateState && project.containers.some(c => containerStatus(c) === 'missing' && isSnoozed(c.monitorSnoozedUntil))) {
    return project.containers.some(c => !(containerStatus(c) === 'missing' && isSnoozed(c.monitorSnoozedUntil)) && statusSeverity(containerStatus(c)) >= 2)
  }
  return statusSeverity(projectHealthStatus(project)) >= 2
}

export function isSnoozed(until?: string, now = Date.now()): boolean {
  return !!until && Date.parse(until) > now
}

export function statusSeverity(status: string): number {
  if (['healthy', 'completed'].includes(status)) return 0
  if (['running-unverified', 'running'].includes(status)) return 1
  if (['starting', 'queued', 'updating', 'refreshing'].includes(status)) return 2
  return 3
}

export function projectHealthStatus(project: Project): string {
  if (project.updateState) return project.updateState === 'running' ? 'updating' : project.updateState
  const statuses = project.containers.map(containerStatus).sort((a, b) => statusSeverity(b) - statusSeverity(a))
  const worst = statuses[0]
  if (worst && statusSeverity(worst) >= statusSeverity(project.status === 'running' ? 'healthy' : project.status)) return worst
  return project.status
}
