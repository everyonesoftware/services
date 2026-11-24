import { useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { activityApplications, activityRuntime, buildActivity, selectActivity } from './activity.ts'
import type { ActivityEntry, ActivityKind, ActivitySort } from './activity.ts'
import { ActivityHelp, OperationNotices, OperationRow } from './DockerActivity.tsx'
import type { MonitorEvent, Operation } from './types.ts'
import { ActivityTimestamp } from './ActivityTimestamp.tsx'
import { PageHeader } from './PageHeader.tsx'
import { FilterSearch, FilterToolbar, IconDropdown, ResetView } from './FilterToolbar.tsx'
import './ActivityFeed.css'

const kindLabels: Record<ActivityKind, string> = { all: 'All activity', operations: 'Operations', events: 'Monitor events' }
const sortLabels: Record<ActivitySort, string> = { newest: 'Newest first', oldest: 'Oldest first', longest: 'Longest first', shortest: 'Shortest first' }
const formatRuntime = (value: number) => {
  const seconds = Math.floor(value / 1000)
  const minutes = Math.floor(seconds / 60)
  return minutes ? `${minutes}m ${String(seconds % 60).padStart(2, '0')}s` : `${seconds}s`
}

function Duration({ children, note }: { children: ReactNode; note: string }) {
  return <span className="activity-duration">{children}<small>{note}</small></span>
}
function FeedEntry({ entry, now }: { entry: ActivityEntry; now: number }) {
  if (entry.kind === 'operation') {
    const runtime = activityRuntime(entry, now)
    return <OperationRow operation={entry.operation} duration={{ text: runtime === null ? '—' : formatRuntime(runtime), note: runtime === null ? entry.operation.state === 'queued' ? 'Not started' : 'Unknown runtime' : entry.operation.state === 'running' ? 'so far' : 'runtime' }} />
  }
  const event = entry.event
  return <article className="activity-event">
    <span className={`activity-event-kind ${event.kind}`}><i />{event.kind.replaceAll('_', ' ')}</span>
    <div className="activity-event-identity"><strong>{event.project || event.name || 'Engine'}</strong><span>Monitor event</span><p>{event.message}</p>{event.name && event.project && <code>{event.name}</code>}</div>
    <Duration note="Point-in-time">—</Duration><span className="activity-event-time"><ActivityTimestamp value={event.time} /></span><span />
  </article>
}

type Props = { operations: Operation[]; events: MonitorEvent[]; operationsLoading: boolean; eventsLoading: boolean; operationsError: string; eventsError: string; now: number; onAcknowledged: () => void }
export function ActivityFeed({ operations, events, operationsLoading, eventsLoading, operationsError, eventsError, now, onAcknowledged }: Props) {
  const [application, setApplication] = useState('')
  const [kind, setKind] = useState<ActivityKind>('all')
  const [sort, setSort] = useState<ActivitySort>('newest')
  const [query, setQuery] = useState('')
  const search = useRef<HTMLInputElement>(null)
  const entries = useMemo(() => buildActivity(operations, events), [operations, events])
  const applications = activityApplications(entries)
  // Keep a previously selected application available even if retained history is pruned.
  if (application && !applications.includes(application)) applications.push(application)
  const selection = selectActivity(entries, { application, kind, sort, query }, now)
  const filtered = !!application || kind !== 'all' || sort !== 'newest' || !!query
  const reset = () => { setApplication(''); setKind('all'); setSort('newest'); setQuery(''); search.current?.focus() }
  const loading = kind === 'operations' ? operationsLoading : kind === 'events' ? eventsLoading : operationsLoading || eventsLoading
  const unavailable = kind === 'operations' ? !!operationsError : kind === 'events' ? !!eventsError : !!operationsError || !!eventsError
  return <main className="workspace unified-activity">
    <PageHeader title="Activity" />
    <OperationNotices operations={operations} error={operationsError} onAcknowledged={onAcknowledged} />
    {eventsError && <p className="notice danger" role="alert">Monitor events unavailable: {eventsError}. Last observed events are shown; current state is unknown.</p>}
    <FilterToolbar label="Activity filters">
      <FilterSearch label="Search activity" inputRef={search} value={query} onChange={setQuery} />
      <div className="activity-icon-group">
        <IconDropdown kind="application" label="Application" value={application} choices={[{ value: '', label: 'All applications' }, ...applications.map(value => ({ value, label: value }))]} active={!!application} onChange={setApplication} />
        <IconDropdown kind="type" label="Activity type" value={kind} choices={Object.entries(kindLabels).map(([value, label]) => ({ value, label }))} active={kind !== 'all'} onChange={value => setKind(value as ActivityKind)} />
        <IconDropdown kind="sort" label="Sort activity" value={sort} choices={Object.entries(sortLabels).map(([value, label]) => ({ value, label }))} active={sort !== 'newest'} onChange={value => setSort(value as ActivitySort)} />
        {filtered && <ResetView onClick={reset} />}
      </div>
    </FilterToolbar>
    {selection.excluded > 0 && <p className="activity-duration-note">Runtime = start to finish; running operations show elapsed time so far. {selection.excluded} matching {selection.excluded === 1 ? 'entry has' : 'entries have'} no comparable runtime and {selection.excluded === 1 ? 'is' : 'are'} hidden (queued work, monitor events, or uncertain results).</p>}
    <ol className="activity-feed" aria-label="Unified activity">{selection.entries.map(entry => <li key={entry.id} data-entry-id={entry.id}><FeedEntry entry={entry} now={now} /></li>)}</ol>
    {!selection.entries.length && <div className="empty-state" role="status"><h3>{loading ? 'Loading activity…' : unavailable ? 'Activity unavailable' : filtered ? 'No matching activity' : 'No activity yet'}</h3><p>{loading ? 'Waiting for operations and monitor events.' : unavailable ? 'Reconnecting automatically. Current activity cannot be confirmed.' : filtered ? 'Try a different search, application, activity type, or sort.' : 'Compose operations and monitor events will appear here.'}</p>{filtered && <button className="button secondary" type="button" onClick={reset}>Reset view</button>}</div>}
    <details className="activity-help"><summary>Filtering & sorting details</summary><p className="activity-scope">Newest/oldest uses finished time, otherwise started or queued time; monitor events use observed time. Longest/shortest compares execution runtime, not queue wait. Entries without a comparable runtime are excluded. Running operations show elapsed time so far.</p></details>
    <ActivityHelp />
  </main>
}
