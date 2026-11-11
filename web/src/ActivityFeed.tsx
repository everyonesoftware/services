import { useEffect, useId, useMemo, useRef, useState } from 'react'
import type { KeyboardEvent, ReactNode } from 'react'
import { activityApplications, activityRuntime, buildActivity, selectActivity } from './activity.ts'
import type { ActivityEntry, ActivityKind, ActivitySort } from './activity.ts'
import { ActivityHelp, OperationNotices, OperationRow } from './DockerActivity.tsx'
import type { MonitorEvent, Operation } from './types.ts'
import { ActivityTimestamp } from './ActivityTimestamp.tsx'
import './ActivityFeed.css'

const kindLabels: Record<ActivityKind, string> = { all: 'All activity', operations: 'Operations', events: 'Monitor events' }
const sortLabels: Record<ActivitySort, string> = { newest: 'Newest first', oldest: 'Oldest first', longest: 'Longest first', shortest: 'Shortest first' }
const formatRuntime = (value: number) => {
  const seconds = Math.floor(value / 1000)
  const minutes = Math.floor(seconds / 60)
  return minutes ? `${minutes}m ${String(seconds % 60).padStart(2, '0')}s` : `${seconds}s`
}

type Choice = { value: string; label: string }
function DropdownIcon({ kind }: { kind: 'application' | 'type' | 'sort' }) {
  return <svg viewBox="0 0 24 24" aria-hidden="true">{kind === 'application' ? <><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" /></> : kind === 'type' ? <><path d="M9 6h12M9 12h12M9 18h12" /><circle cx="4" cy="6" r="1" /><circle cx="4" cy="12" r="1" /><circle cx="4" cy="18" r="1" /></> : <path d="M5 4v16m-3-3 3 3 3-3M12 5h9M12 10h7M12 15h5M12 20h3" />}</svg>
}

function IconDropdown({ kind, label, value, choices, active, onChange }: { kind: 'application' | 'type' | 'sort'; label: string; value: string; choices: Choice[]; active: boolean; onChange: (value: string) => void }) {
  const id = useId()
  const button = useRef<HTMLButtonElement>(null)
  const menu = useRef<HTMLDivElement>(null)
  const anchor = useRef<{ top: number; left: number } | null>(null)
  const [expanded, setExpanded] = useState(false)
  const selected = choices.find(choice => choice.value === value)?.label ?? value
  const title = `${label}: ${selected}`
  const close = (restoreFocus = true) => {
    if (menu.current?.matches(':popover-open')) menu.current.hidePopover()
    if (restoreFocus) button.current?.focus({ preventScroll: true })
  }
  const open = (last = false) => {
    if (!menu.current || !button.current) return
    if (menu.current.matches(':popover-open')) { close(); return }
    const rect = button.current.getBoundingClientRect()
    anchor.current = { top: rect.top, left: rect.left }
    const width = Math.min(280, window.innerWidth - 24)
    const below = window.innerHeight - rect.bottom - 19
    const above = rect.top - 19
    const useAbove = below < 200 && above > below
    Object.assign(menu.current.style, {
      width: `${width}px`, left: `${Math.max(12, Math.min(rect.right - width, window.innerWidth - width - 12))}px`,
      top: useAbove ? 'auto' : `${rect.bottom + 7}px`, bottom: useAbove ? `${window.innerHeight - rect.top + 7}px` : 'auto',
      maxHeight: `${Math.max(44, useAbove ? above : below)}px`,
    })
    menu.current.showPopover()
    const options = [...menu.current.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')]
    ;(last ? options.at(-1) : options.find(option => option.getAttribute('aria-checked') === 'true') ?? options[0])?.focus({ preventScroll: true })
  }
  const keydown = (event: KeyboardEvent<HTMLDivElement>) => {
    const options = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')]
    const index = options.indexOf(document.activeElement as HTMLButtonElement)
    const target = event.key === 'ArrowDown' ? options[(index + 1) % options.length] : event.key === 'ArrowUp' ? options[(index - 1 + options.length) % options.length] : event.key === 'Home' ? options[0] : event.key === 'End' ? options.at(-1) : undefined
    if (target) { event.preventDefault(); target.focus() }
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close() }
    if (event.key === 'Tab') close(false)
  }
  useEffect(() => {
    const moved = (event: Event) => {
      if (event.target instanceof Node && menu.current?.contains(event.target)) return
      if (!menu.current?.matches(':popover-open')) return
      const rect = button.current?.getBoundingClientRect()
      // A focus-induced scroll event can arrive after opening. Close only when the anchor actually moves.
      if (event.type === 'resize' || !rect || !anchor.current || Math.abs(rect.top - anchor.current.top) > 1 || Math.abs(rect.left - anchor.current.left) > 1) menu.current.hidePopover()
    }
    window.addEventListener('resize', moved)
    document.addEventListener('scroll', moved, true)
    return () => { window.removeEventListener('resize', moved); document.removeEventListener('scroll', moved, true) }
  }, [])
  return <>
    <button ref={button} className="activity-icon-dropdown" type="button" title={title} aria-label={title} aria-haspopup="menu" aria-expanded={expanded} aria-controls={id} data-active={active} onClick={() => open()} onKeyDown={event => { if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); open(event.key === 'ArrowUp') } }}><DropdownIcon kind={kind} /><svg className="activity-chevron" viewBox="0 0 24 24" aria-hidden="true"><path d="m7 10 5 5 5-5" /></svg></button>
    <div ref={menu} id={id} popover="auto" className="activity-dropdown-menu" role="menu" aria-label={`${label} options`} onToggle={event => setExpanded(event.currentTarget.matches(':popover-open'))} onKeyDown={keydown}>
      <p className="activity-menu-title">{label}</p>
      {choices.map(choice => <button key={choice.value} className="activity-menu-option" type="button" role="menuitemradio" tabIndex={-1} aria-checked={choice.value === value} onClick={() => { close(); onChange(choice.value) }}><span>{choice.label}</span><span aria-hidden="true">{choice.value === value ? '✓' : ''}</span></button>)}
    </div>
  </>
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
    <header className="page-heading"><div><h1>Activity</h1><p className="page-description">Compose operations and monitor events, in one feed.</p></div></header>
    <OperationNotices operations={operations} error={operationsError} onAcknowledged={onAcknowledged} />
    {eventsError && <p className="notice danger" role="alert">Monitor events unavailable: {eventsError}. Last observed events are shown; current state is unknown.</p>}
    <section className="activity-filters" aria-label="Activity filters">
      <div className="activity-search"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10" cy="10" r="6" /><path d="m15 15 5 5" /></svg><input ref={search} type="search" aria-label="Search activity" placeholder="Search activity…" value={query} onChange={event => setQuery(event.target.value)} />{query && <button type="button" aria-label="Clear search" title="Clear search" onClick={() => { setQuery(''); search.current?.focus() }}>×</button>}</div>
      <div className="activity-icon-group">
        <IconDropdown kind="application" label="Application" value={application} choices={[{ value: '', label: 'All applications' }, ...applications.map(value => ({ value, label: value }))]} active={!!application} onChange={setApplication} />
        <IconDropdown kind="type" label="Activity type" value={kind} choices={Object.entries(kindLabels).map(([value, label]) => ({ value, label }))} active={kind !== 'all'} onChange={value => setKind(value as ActivityKind)} />
        <IconDropdown kind="sort" label="Sort activity" value={sort} choices={Object.entries(sortLabels).map(([value, label]) => ({ value, label }))} active={sort !== 'newest'} onChange={value => setSort(value as ActivitySort)} />
        {filtered && <button className="activity-icon-dropdown" type="button" aria-label="Reset view" title="Reset view" onClick={reset}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 10a9 9 0 1 1 2 8M3 4v6h6" /></svg></button>}
      </div>
    </section>
    {selection.excluded > 0 && <p className="activity-duration-note">Runtime = start to finish; running operations show elapsed time so far. {selection.excluded} matching {selection.excluded === 1 ? 'entry has' : 'entries have'} no comparable runtime and {selection.excluded === 1 ? 'is' : 'are'} hidden (queued work, monitor events, or uncertain results).</p>}
    <ol className="activity-feed" aria-label="Unified activity">{selection.entries.map(entry => <li key={entry.id} data-entry-id={entry.id}><FeedEntry entry={entry} now={now} /></li>)}</ol>
    {!selection.entries.length && <div className="empty-state" role="status"><h3>{loading ? 'Loading activity…' : unavailable ? 'Activity unavailable' : filtered ? 'No matching activity' : 'No activity yet'}</h3><p>{loading ? 'Waiting for operations and monitor events.' : unavailable ? 'Reconnecting automatically. Current activity cannot be confirmed.' : filtered ? 'Try a different search, application, activity type, or sort.' : 'Compose operations and monitor events will appear here.'}</p>{filtered && <button className="button secondary" type="button" onClick={reset}>Reset view</button>}</div>}
    <details className="activity-help"><summary>Filtering & sorting details</summary><p className="activity-scope">Newest/oldest uses finished time, otherwise started or queued time; monitor events use observed time. Longest/shortest compares execution runtime, not queue wait. Entries without a comparable runtime are excluded. Running operations show elapsed time so far.</p></details>
    <ActivityHelp />
  </main>
}
