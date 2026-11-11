import { useState } from 'react'
import { PublicationDetails } from './PublicationDetails.tsx'
import { availableServices, canUpdateProject, containerStatus, isSnoozed, needsAttention, projectHealthStatus, statusSeverity } from './monitor.ts'
import type { Container, Icon, Project } from './types.ts'
import { iconForImage, iconForProject } from './brandIcons.js'
import { ServiceIcon } from './ServiceIcon.tsx'
import type { IconEditorTarget } from './ServiceIcon.tsx'

type IconEditingProps = { icons?: Icon[]; iconsAvailable?: boolean; onEditIcon?: (target: IconEditorTarget) => void }
type ResolutionProps = { onResolve?: (container: Container, trigger: HTMLElement) => void; resolveDisabled?: boolean }

const labels: Record<string, string> = { queued: 'Queued', updating: 'Updating', refreshing: 'Refreshing', healthy: 'Healthy', completed: 'Completed', 'running-unverified': 'No health check', 'restart-loop': 'Restart loop', 'oom-killed': 'Out of memory', missing: 'Missing', unhealthy: 'Unhealthy', starting: 'Starting', restarting: 'Restarting', stopped: 'Stopped', exited: 'Exited', running: 'Running' }
export function StatusBadge({ status }: { status: string }) {
  const tone = statusSeverity(status) === 0 ? 'good' : statusSeverity(status) < 3 ? 'caution' : 'bad'
  return <span className={`status-badge ${tone}`}><i />{labels[status] || status}</span>
}

export function ProjectGroup({ project, onReview, actionsDisabled, icons = [], iconsAvailable = false, onEditIcon, onResolve, resolveDisabled }: { project: Project; onReview: (project: Project) => void; actionsDisabled: boolean } & IconEditingProps & ResolutionProps) {
  const automatic = iconForProject(project)
  const current = icons.find(icon => icon.kind === 'project' && icon.key === project.name)
  const [open, setOpen] = useState(() => needsAttention(project))
  const updates = availableServices(project)
  const eligible = canUpdateProject(project)
  const status = projectHealthStatus(project)
  const services = new Set(project.containers.map(c => c.service || c.name)).size
  return <details className={`project-group ${needsAttention(project) ? 'has-problem' : ''}`} open={open} onToggle={event => setOpen(event.currentTarget.open)}>
    <summary className="group-summary"><span className="disclosure" aria-hidden="true">›</span><span className="group-identity"><strong className="identity-name"><ServiceIcon icon={automatic} custom={current} project label={`Edit ${project.name} project icon`} onEdit={iconsAvailable && onEditIcon ? trigger => onEditIcon({ kind: 'project', key: project.name, automatic, current, trigger }) : undefined} /><span>{project.name}</span></strong><span>{services} {services === 1 ? 'service' : 'services'} <b>·</b> {project.containers.length} {project.containers.length === 1 ? 'container' : 'containers'}</span></span><span className="group-signals">{updates.length > 0 && <span className="update-label">{updates.length} {updates.length === 1 ? 'update' : 'updates'}</span>}<StatusBadge status={status} /></span></summary>
    <div className="group-body">
      {updates.length > 0 && <div className="group-update"><span>{eligible ? 'New images available.' : 'Updates require valid Compose configuration and service labels.'}</span><button type="button" className="button update-button" disabled={actionsDisabled || !eligible} onClick={() => onReview(project)}>Review {updates.length} {updates.length === 1 ? 'update' : 'updates'} <span aria-hidden="true">↗</span></button></div>}
      {project.containers.map(container => <ContainerRow key={container.id} container={project.updateState === 'refreshing' ? { ...container, updateAvailable: false, updatePublication: undefined, updateStatus: undefined, updateError: undefined, updateCheckedAt: undefined } : container} icons={icons} iconsAvailable={iconsAvailable} onEditIcon={onEditIcon} onResolve={onResolve} resolveDisabled={resolveDisabled ?? actionsDisabled} />)}
    </div>
  </details>
}

function ContainerRow({ container, icons = [], iconsAvailable = false, onEditIcon, onResolve, resolveDisabled }: { container: Container } & IconEditingProps & ResolutionProps) {
  const automatic = iconForImage(container.image)
  const current = icons.find(icon => icon.kind === 'container' && icon.key === container.name)
  const status = containerStatus(container)
  const snoozed = status === 'missing' && isSnoozed(container.monitorSnoozedUntil)
  const problem = statusSeverity(status) > 0
  return <details className="container-detail">
    <summary className="container-summary"><span className={`container-dot ${statusSeverity(status) === 0 ? 'good' : statusSeverity(status) === 3 ? 'bad' : 'caution'}`} /><div className="container-identity"><strong className="identity-name"><ServiceIcon icon={automatic} custom={current} label={`Edit ${container.name} container icon`} onEdit={iconsAvailable && onEditIcon ? trigger => onEditIcon({ kind: 'container', key: container.name, automatic, current, trigger }) : undefined} /><span>{container.service || container.name}</span></strong>{container.name !== (container.service || container.name) && <span>{container.name}</span>}</div><div className="container-observation">{problem && <p>{container.monitorReason || `${container.state}${container.health ? ` · ${container.health}` : ''}`}</p>}<code>{container.image || 'Image not reported'}</code></div><span className="container-end"><StatusBadge status={snoozed ? 'Missing · Snoozed' : status} />{status === 'missing' && onResolve && <button type="button" className="button secondary row-resolve" disabled={resolveDisabled} onClick={event => { event.preventDefault(); event.stopPropagation(); onResolve(container, event.currentTarget) }}>Resolve…</button>}<span className="disclosure" aria-hidden="true">›</span></span></summary>
    <div className={`container-inspection ${problem ? 'problem-inspection' : ''}`}><dl className="container-facts"><div><dt>State</dt><dd>{container.state}</dd></div><div><dt>Health check</dt><dd>{container.health || 'Not configured'}</dd></div><div><dt>Restarts</dt><dd>{container.restartCount ?? 'Not reported'}</dd></div><div><dt>Exit code</dt><dd>{container.exitCode ?? 'Not reported'}</dd></div><div><dt>OOM killed</dt><dd>{container.oomKilled === undefined ? 'Not reported' : container.oomKilled ? 'Yes' : 'No'}</dd></div><div><dt>Started</dt><dd>{container.startedAt && Number.isFinite(Date.parse(container.startedAt)) ? new Date(container.startedAt).toLocaleString() : 'Not reported'}</dd></div></dl><div className="inspection-line"><span>Container</span><code>{container.id}</code></div><div className="inspection-line"><span>Image</span><code>{container.image || 'Not reported'}</code></div><div className="health-output"><h4>Health output</h4><pre>{container.healthOutput || 'No health-check output reported.'}</pre></div><PublicationDetails containers={[container]} />{container.updateError && <p className="notice danger">Image check: {container.updateError}</p>}{container.updateStatus && <p className="image-check-note">Image check: {container.updateStatus}{container.updateCheckedAt ? ` · ${new Date(container.updateCheckedAt).toLocaleString()}` : ''}</p>}</div>
  </details>
}
