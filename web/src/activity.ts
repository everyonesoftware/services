import type { MonitorEvent, Operation } from './types.ts'

export type ActivityKind = 'all' | 'operations' | 'events'
export type ActivitySort = 'newest' | 'oldest' | 'longest' | 'shortest'
export type ActivityEntry =
  | { id: string; kind: 'operation'; application: string; timestamp: number | null; operation: Operation }
  | { id: string; kind: 'event'; application: string; timestamp: number | null; event: MonitorEvent }

function date(value: string | undefined): number | null {
  const timestamp = value ? Date.parse(value) : NaN
  return Number.isFinite(timestamp) ? timestamp : null
}

function compareId(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0
}

function newest(a: ActivityEntry, b: ActivityEntry): number {
  if (a.timestamp === null) return b.timestamp === null ? compareId(a.id, b.id) : 1
  if (b.timestamp === null) return -1
  return b.timestamp - a.timestamp || compareId(a.id, b.id)
}

function recordKey(entry: ActivityEntry): string {
  const record = entry.kind === 'operation' ? entry.operation : entry.event
  return JSON.stringify(record, Object.keys(record).sort())
}

export function activityRuntime(entry: ActivityEntry, now: number): number | null {
  if (entry.kind !== 'operation' || !Number.isFinite(now)) return null
  const operation = entry.operation
  if (operation.state === 'queued' || operation.phase === 'interrupted' || operation.needsAttention) return null
  const start = date(operation.startedAt)
  const finish = operation.finishedAt ? date(operation.finishedAt) : operation.state === 'running' ? now : null
  if (start === null || finish === null || start > now || finish > now || finish < start) return null
  return finish - start
}

export function activityApplications(entries: ActivityEntry[]): string[] {
  return [...new Set(entries.map(entry => entry.application).filter(application => application !== ''))].sort(compareId)
}

export function selectActivity(
  entries: ActivityEntry[],
  options: { application: string; kind: ActivityKind; query: string; sort: ActivitySort },
  now: number,
): { entries: ActivityEntry[]; excluded: number } {
  const query = options.query.trim().toLowerCase()
  const matching = entries.filter(entry => {
    if (options.application !== '' && entry.application !== options.application) return false
    if (options.kind === 'operations' && entry.kind !== 'operation') return false
    if (options.kind === 'events' && entry.kind !== 'event') return false
    const fields = entry.kind === 'operation'
      ? [entry.application, entry.operation.action, entry.operation.error ?? '', ...entry.operation.messages]
      : [entry.application, entry.event.name, entry.event.kind, entry.event.message]
    return fields.some(field => field.toLowerCase().includes(query))
  })
  if (options.sort === 'longest' || options.sort === 'shortest') {
    const timed = matching.map(entry => ({ entry, runtime: activityRuntime(entry, now) }))
      .filter((item): item is { entry: ActivityEntry; runtime: number } => item.runtime !== null)
    timed.sort((a, b) => (options.sort === 'longest' ? b.runtime - a.runtime : a.runtime - b.runtime) || newest(a.entry, b.entry))
    return { entries: timed.map(item => item.entry), excluded: matching.length - timed.length }
  }
  matching.sort((a, b) => {
    if (a.timestamp === null || b.timestamp === null || options.sort === 'newest') return newest(a, b)
    return a.timestamp - b.timestamp || compareId(a.id, b.id)
  })
  return { entries: matching, excluded: 0 }
}

export function buildActivity(operations: Operation[], events: MonitorEvent[]): ActivityEntry[] {
  const entries: ActivityEntry[] = [
    ...operations.map((operation): ActivityEntry => ({ id: `operation:${operation.id}`, kind: 'operation', application: operation.project || '', timestamp: date(operation.finishedAt) ?? date(operation.startedAt) ?? date(operation.queuedAt), operation })),
    ...events.map((event): ActivityEntry => ({ id: `event:${event.id}`, kind: 'event', application: event.project || '', timestamp: date(event.time), event })),
  ]
  const unique = new Map<string, ActivityEntry>()
  for (const entry of entries) {
    const previous = unique.get(entry.id)
    // Prefer the newest snapshot; equal-time conflicts use a stable record key.
    if (!previous || newest(entry, previous) < 0 || (newest(entry, previous) === 0 && compareId(recordKey(entry), recordKey(previous)) < 0)) unique.set(entry.id, entry)
  }
  // Keep construction order deterministic; presentation ordering belongs to selectActivity.
  return [...unique.values()].sort((a, b) => {
    if (a.kind !== b.kind) return a.kind === 'operation' ? -1 : 1
    return compareId(a.id, b.id)
  })
}
