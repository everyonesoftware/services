export type LogEntry = { id: string; timestamp: string; containerId: string; container: string; stream: 'stdout' | 'stderr' | 'tty'; message: string }
export type LogPage = { entries: LogEntry[]; hasMore: boolean; nextCursor?: string; nextAfter?: string; hasNewer?: boolean; warnings?: { containerId: string; container: string; message: string }[] }
export type LogScope = { project: string; container?: string; cursor?: string; after?: string }

export class LogCursorError extends Error {
  override name = 'LogCursorError'
}

// Keep server order: nanosecond timestamps and opaque IDs cannot be sorted safely as JS dates.
export function mergeLogEntries(current: LogEntry[], incoming: LogEntry[], direction: 'older' | 'newer', limit = 10_000): { entries: LogEntry[]; trimmed: boolean } {
  const seen = new Set<string>()
  const combined = (direction === 'older' ? [...incoming, ...current] : [...current, ...incoming]).filter(entry => { if (seen.has(entry.id)) return false; seen.add(entry.id); return true })
  let size = 0
  const entries: LogEntry[] = []
  const ordered = direction === 'older' ? combined : [...combined].reverse()
  for (const entry of ordered) {
    size += entry.id.length + entry.timestamp.length + entry.containerId.length + entry.container.length + entry.message.length
    if (entries.length >= limit || size > 8 * 1024 * 1024) break
    entries.push(entry)
  }
  if (direction === 'newer') entries.reverse()
  return { entries, trimmed: entries.length < combined.length }
}

export function selectLogEntries(entries: LogEntry[], filters: { query?: string; stream?: string; source?: string }): LogEntry[] {
  const query = filters.query?.trim().toLocaleLowerCase() || ''
  return entries.filter(entry => (!filters.stream || entry.stream === filters.stream) && (!filters.source || entry.containerId === filters.source) && (!query || `${entry.timestamp} ${entry.container} ${entry.stream} ${entry.message}`.toLocaleLowerCase().includes(query)))
}

export function logDownload(entries: LogEntry[]): string {
  return entries.map(entry => `${entry.timestamp} ${entry.container} ${entry.stream} ${entry.message}`).join('\n') + (entries.length ? '\n' : '')
}

export async function fetchLogs(scope: LogScope, signal: AbortSignal, fetcher: typeof fetch = fetch): Promise<LogPage> {
  if (scope.cursor && scope.after) throw new Error('cursor and after are mutually exclusive')
  const params = new URLSearchParams({ project: scope.project, ...(scope.container === undefined ? {} : { container: scope.container }), limit: '200', ...(scope.cursor ? { cursor: scope.cursor } : {}), ...(scope.after ? { after: scope.after } : {}) })
  const response = await fetcher(`/api/logs?${params}`, { method: 'GET', cache: 'no-store', signal })
  if (!response.ok) {
    const message = (await response.text()).trim() || `Logs unavailable (${response.status})`
    if ((scope.cursor || scope.after) && [400, 409].includes(response.status)) throw new LogCursorError(message)
    throw new Error(message)
  }
  const page = await response.json() as LogPage
  if (!page || !Array.isArray(page.entries) || typeof page.hasMore !== 'boolean' || page.entries.some(entry => !entry || (['id', 'timestamp', 'containerId', 'container', 'message'] as const).some(key => typeof entry[key] !== 'string') || !['stdout', 'stderr', 'tty'].includes(entry.stream))) throw new Error('Invalid logs response')
  if (page.entries.reduce((size, entry) => size + entry.id.length + entry.timestamp.length + entry.containerId.length + entry.container.length + entry.message.length, 0) > 8 * 1024 * 1024) throw new Error('Log page exceeded the viewer safety limit; select one container and retry')
  return page
}
