import type { ReactNode } from 'react'
import { Breadcrumb } from './Breadcrumb.tsx'
import { ContainerArtwork, ProjectArtwork, ProjectUpdateButton, StatusBadge } from './ProjectGroup.tsx'
import type { ProjectGroupProps } from './ProjectGroup.tsx'
import { PublicationDetails } from './PublicationDetails.tsx'
import { availableServices, canUpdateProject, containerStatus, isSnoozed, projectHealthStatus, recentEvents, statusSeverity } from './monitor.ts'
import { serviceHref } from './navigation.ts'
import type { Container, MonitorEvent, Project } from './types.ts'
import './ServiceDetails.css'
import { LogsDetailTabs } from './Logs.tsx'
import { PublisherLinks } from './PublisherReconciliation.tsx'
import { publishersForContainers } from './publisherReconciliation.ts'

type DetailProps = ProjectGroupProps & { events?: MonitorEvent[]; eventsLoading?: boolean; eventsError?: string; feedback?: ReactNode }
const dateLabel = (value?: string) => value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : 'Not reported'
const currentContainer = (project: Project, container: Container): Container => project.updateState === 'refreshing' ? { ...container, updateAvailable: false, updatePublication: undefined, updateStatus: undefined, updateError: undefined, updateCheckedAt: undefined, proposedDigest: undefined } : container
const observedStatus = (container: Container) => containerStatus(container) === 'missing' && isSnoozed(container.monitorSnoozedUntil) ? 'Missing · Snoozed' : containerStatus(container)

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return <div><dt>{label}</dt><dd>{children}</dd></div>
}

function ResolveButton({ container, onResolve, resolveDisabled, actionsDisabled }: Pick<DetailProps, 'onResolve' | 'resolveDisabled' | 'actionsDisabled'> & { container: Container }) {
  return containerStatus(container) === 'missing' && onResolve ? <button type="button" className="button secondary row-resolve" disabled={resolveDisabled ?? actionsDisabled} onClick={event => onResolve(container, event.currentTarget)}>Resolve…</button> : null
}

function ContainerLink({ container, ...props }: DetailProps & { container: Container }) {
  const observed = currentContainer(props.project, container)
  return <article className="container-row"><div className="container-summary">
    <ContainerArtwork {...props} container={observed} />
    <a className="container-link" href={serviceHref(props.project.name, observed.name)}><span className="container-link-copy"><strong className="identity-name"><span>{observed.name}</span></strong><span className="container-observation"><code>{observed.image || 'Image not reported'}</code>{statusSeverity(containerStatus(observed)) >= 2 && <span className="container-reason">{observed.monitorReason || `${observed.state}${observed.health ? ` · ${observed.health}` : ''}`}</span>}</span><span className="container-link-meta"><StatusBadge status={observedStatus(observed)} />{observed.updateAvailable && !props.project.updateState && <span className="container-update-note">Image update available</span>}</span></span><span className="navigation-arrow" aria-hidden="true">›</span></a>
    <ResolveButton {...props} container={observed} />
  </div></article>
}

function ServiceEvents({ project, events = [], eventsLoading = false, eventsError = '' }: Pick<DetailProps, 'project' | 'events' | 'eventsLoading' | 'eventsError'>) {
  const scoped = recentEvents(events.filter(event => event.project === project.name), 6)
  return <section className="detail-section" aria-labelledby="service-activity-title"><div className="section-title"><h2 id="service-activity-title">Recent activity</h2><span className="section-note">This service only</span></div>
    {eventsError && <p className="notice danger">Activity unavailable: {eventsError}. {scoped.length > 0 && 'Last observed events are shown.'}</p>}
    {eventsLoading && scoped.length === 0 ? <p className="detail-empty" role="status">Loading activity…</p> : scoped.length > 0 ? <ul className="service-events detail-panel">{scoped.map(event => <li key={event.id}><span className={`event-dot ${event.kind}`} aria-hidden="true" /><div><strong>{event.name}</strong><p>{event.message || event.kind}</p><time dateTime={event.time}>{dateLabel(event.time)}</time></div></li>)}</ul> : !eventsError && <p className="detail-empty">No recent activity reported.</p>}
  </section>
}

export function ServiceDetail(props: DetailProps) {
  const { project, feedback } = props
  const updates = availableServices(project)
  const services = new Set(project.containers.map(container => container.service).filter(Boolean))
  return <main className="workspace service-detail">
    <Breadcrumb />
    <header className="detail-header"><div className="detail-identity"><ProjectArtwork {...props} /><div><h1>{project.name}</h1><p>{project.name === 'Standalone' ? 'Ungrouped containers' : 'Collected service'} · {project.containers.length} {project.containers.length === 1 ? 'container' : 'containers'}</p></div></div><div className="heading-actions"><ProjectUpdateButton {...props} /></div></header>
    {feedback}
    <div className="detail-status"><StatusBadge status={projectHealthStatus(project)} /><span>{project.updateState === 'refreshing' ? 'Refreshing inventory after Compose work.' : project.updateState ? 'Compose work is tracked in Activity.' : 'Status reflects the latest observed container health.'}</span></div>
    <LogsDetailTabs key={project.name} project={project}><div className="detail-grid"><div>
      <section className="detail-section" aria-labelledby="service-containers-title"><div className="section-title"><h2 id="service-containers-title">Containers <span className="count">{project.containers.length}</span></h2><span className="section-note">Open to inspect</span></div><div className="service-containers">{project.containers.map(container => <ContainerLink key={container.name} {...props} container={container} />)}</div>{project.containers.length === 0 && <p className="detail-empty">No containers reported for this service.</p>}</section>
      <PublisherLinks connections={props.publishers ?? []} />
      <ServiceEvents {...props} />
    </div><aside>
      <section className="detail-section" aria-labelledby="service-details-title"><div className="section-title"><h2 id="service-details-title">Service details</h2></div><dl className="detail-panel detail-facts"><Fact label="Compose project">{project.name === 'Standalone' ? 'Not grouped by Docker Compose' : <code>{project.name}</code>}</Fact><Fact label="Compose services">{services.size || 'Not reported'}</Fact><Fact label="Containers">{project.containers.length}</Fact><Fact label="Monitoring">Container health and image changes</Fact></dl></section>
      <section className="detail-section" aria-labelledby="service-images-title"><div className="section-title"><h2 id="service-images-title">Image updates</h2></div><dl className="detail-panel detail-facts"><Fact label="Available">{project.updateState ? <StatusBadge status={projectHealthStatus(project)} /> : updates.length ? `${updates.length} ${updates.length === 1 ? 'service has' : 'services have'} a new image` : 'No image updates reported'}</Fact><Fact label="Update scope">{project.name === 'Standalone' ? 'Compose-managed updates are unavailable.' : 'Review changes for this Compose project before updating.'}</Fact></dl>{updates.length > 0 && !canUpdateProject(project) && <p className="project-update-help">Updates require valid Compose configuration and service labels.</p>}</section>
      <a className="detail-activity-link" href="#activity">Open Activity <span aria-hidden="true">↗</span></a>
    </aside></div></LogsDetailTabs>
  </main>
}

export function ContainerDetail({ container: rawContainer, ...props }: DetailProps & { container: Container }) {
  const { project, feedback } = props
  const container = currentContainer(project, rawContainer)
  const siblings = project.containers.filter(sibling => sibling.name !== container.name)
  return <main className="workspace service-detail container-page">
    <Breadcrumb parentProject={project.name} />
    <header className="detail-header"><div className="detail-identity"><ContainerArtwork {...props} container={container} /><div><h1>{container.name}</h1><p>Container{container.service ? ` · ${container.service}` : ''}</p></div></div><div className="heading-actions"><ResolveButton {...props} container={container} /><a className="button secondary" href={serviceHref(project.name)}>View service <span aria-hidden="true">↗</span></a></div></header>
    {feedback}
    <div className="detail-status"><StatusBadge status={observedStatus(container)} /><span>{container.monitorReason || (container.health ? `Health check: ${container.health}` : 'No health check is configured; running does not confirm application health.')}</span></div>
    <LogsDetailTabs key={`${project.name}/${container.id}`} project={project} container={container.id}><div className="detail-grid container-inspection"><div>
      <section className="detail-section" aria-labelledby="container-runtime-title"><div className="section-title"><h2 id="container-runtime-title">Runtime</h2></div><dl className="detail-panel container-facts runtime-facts"><Fact label="State">{container.state}</Fact><Fact label="Health check">{container.health || 'Not configured'}</Fact><Fact label="Restarts">{container.restartCount ?? 'Not reported'}</Fact><Fact label="Exit code">{container.exitCode ?? 'Not reported'}</Fact><Fact label="OOM killed">{container.oomKilled === undefined ? 'Not reported' : container.oomKilled ? 'Yes' : 'No'}</Fact><Fact label="Started">{dateLabel(container.startedAt)}</Fact></dl></section>
      <PublisherLinks connections={publishersForContainers(props.publishers ?? [], [container])} />
      <section className="detail-section health-output" aria-labelledby="container-health-title"><div className="section-title"><h2 id="container-health-title">Health output</h2><span className="section-note">Latest reported check</span></div><pre>{container.healthOutput || 'No health-check output reported.'}</pre></section>
      <section className="detail-section" aria-labelledby="container-image-title"><div className="section-title"><h2 id="container-image-title">Image</h2></div><div className="detail-panel detail-image"><dl className="detail-facts"><Fact label="Image reference"><code>{container.image || 'Not reported'}</code></Fact><Fact label="Current digest"><code>{container.currentDigest || 'Not reported'}</code></Fact><Fact label="Update status">{project.updateState === 'refreshing' ? 'Refreshing' : container.updateStatus || (container.updateAvailable ? 'New image available' : 'No update reported')}</Fact><Fact label="Image checked">{dateLabel(container.updateCheckedAt)}</Fact></dl><PublicationDetails containers={[container]} />{container.updateError && <p className="notice danger">Image check: {container.updateError}</p>}</div>{container.updateAvailable && !project.updateState && <div className="detail-update-callout"><strong>Review this update through the collected service</strong><p>Affected containers are recreated together through Docker Compose.</p><ProjectUpdateButton {...props} /></div>}</section>
    </div><aside>
      <section className="detail-section" aria-labelledby="container-details-title"><div className="section-title"><h2 id="container-details-title">Container details</h2></div><dl className="detail-panel detail-facts"><Fact label="Name"><code>{container.name}</code></Fact><Fact label="Container ID"><code>{container.id}</code></Fact><Fact label="Collected service"><a href={serviceHref(project.name)}>{project.name} ↗</a></Fact><Fact label="Compose service"><code>{container.service || 'Not reported'}</code></Fact>{isSnoozed(container.monitorSnoozedUntil) && <Fact label="Alerts snoozed until">{dateLabel(container.monitorSnoozedUntil)}</Fact>}</dl></section>
      <section className="detail-section container-siblings" aria-labelledby="container-siblings-title"><div className="section-title"><h2 id="container-siblings-title">Other containers</h2></div><div className="service-containers">{siblings.map(sibling => <ContainerLink key={sibling.name} {...props} container={sibling} />)}</div>{siblings.length === 0 && <p className="detail-empty">Only container in this service.</p>}</section>
    </aside></div></LogsDetailTabs>
  </main>
}
