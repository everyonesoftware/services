import './ProjectGroup.css'
import { availableServices, canUpdateProject, needsAttention, projectHealthStatus, statusSeverity } from './monitor.ts'
import type { Container, Icon, Project } from './types.ts'
import { iconForImage, iconForProject } from './brandIcons.js'
import { ServiceIcon } from './ServiceIcon.tsx'
import type { IconEditorTarget } from './ServiceIcon.tsx'
import { serviceHref } from './navigation.ts'
import { PublisherLinks } from './PublisherReconciliation.tsx'
import type { Connection } from './publisherApi.ts'

export type IconEditingProps = { icons?: Icon[]; iconsAvailable?: boolean; onEditIcon?: (target: IconEditorTarget) => void }
export type ResolutionProps = { onResolve?: (container: Container, trigger: HTMLElement) => void; resolveDisabled?: boolean }
export type ProjectGroupProps = { project: Project; onReview: (project: Project) => void; actionsDisabled: boolean; publishers?: Connection[] } & IconEditingProps & ResolutionProps

const labels: Record<string, string> = { queued: 'Queued', updating: 'Updating', refreshing: 'Refreshing', healthy: 'Healthy', completed: 'Completed', 'running-unverified': 'No health check', 'restart-loop': 'Restart loop', 'oom-killed': 'Out of memory', missing: 'Missing', unhealthy: 'Unhealthy', starting: 'Starting', restarting: 'Restarting', stopped: 'Stopped', exited: 'Exited', running: 'Running' }
export function StatusBadge({ status }: { status: string }) {
  const tone = statusSeverity(status) === 0 ? 'good' : statusSeverity(status) < 3 ? 'caution' : 'bad'
  return <span className={`status-badge ${tone}`}><i />{labels[status] || status}</span>
}

export function ProjectArtwork({ project, icons = [], iconsAvailable = false, onEditIcon }: { project: Project } & IconEditingProps) {
  const automatic = iconForProject(project)
  const current = icons.find(icon => icon.kind === 'project' && icon.key === project.name)
  return <ServiceIcon icon={automatic} custom={current} project label={`Edit ${project.name} project icon`} onEdit={iconsAvailable && onEditIcon ? trigger => onEditIcon({ kind: 'project', key: project.name, automatic, current, trigger }) : undefined} />
}

export function ContainerArtwork({ container, icons = [], iconsAvailable = false, onEditIcon }: { container: Container } & IconEditingProps) {
  const automatic = iconForImage(container.image)
  const current = icons.find(icon => icon.kind === 'container' && icon.key === container.name)
  return <ServiceIcon icon={automatic} custom={current} label={`Edit ${container.name} container icon`} onEdit={iconsAvailable && onEditIcon ? trigger => onEditIcon({ kind: 'container', key: container.name, automatic, current, trigger }) : undefined} />
}

export function ProjectUpdateButton({ project, onReview, actionsDisabled }: ProjectGroupProps) {
  const updates = availableServices(project)
  const eligible = canUpdateProject(project)
  return updates.length > 0 ? <button type="button" className="button secondary project-update" aria-label={`Review ${updates.length} ${updates.length === 1 ? 'update' : 'updates'}`} title={eligible ? 'Review available image updates' : 'Updates require valid Compose configuration and service labels.'} disabled={actionsDisabled || !eligible} onClick={() => onReview(project)}>{updates.length} {updates.length === 1 ? 'update' : 'updates'} <span aria-hidden="true">↗</span></button> : null
}

export function ProjectGroup(props: ProjectGroupProps) {
  const { project } = props
  const status = projectHealthStatus(project)
  return <article className={`project-group ${needsAttention(project) ? 'has-problem' : ''}`}>
    <div className="group-summary project-summary">
      <ProjectArtwork {...props} />
      <a className="project-link" href={serviceHref(project.name)}><span className="group-identity"><strong className="identity-name"><span>{project.name}</span></strong><span className="project-metadata"><span className="project-summary-counts">{project.containers.length} {project.containers.length === 1 ? 'container' : 'containers'}</span><span className="group-signals project-health"><StatusBadge status={status} /></span></span></span><span className="navigation-arrow" aria-hidden="true">›</span></a>
      <span className="group-signals project-signals"><ProjectUpdateButton {...props} /></span>
    </div>
    <PublisherLinks connections={props.publishers ?? []} />
  </article>
}
