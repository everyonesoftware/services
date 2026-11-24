import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import type { KeyboardEvent, ReactNode } from 'react'
import type { Project } from './types.ts'
import { fetchLogs, LogCursorError, logDownload, mergeLogEntries, selectLogEntries } from './logs.ts'
import type { LogEntry, LogPage } from './logs.ts'
import './Logs.css'

function visibleLogPosition(output: HTMLElement) {
  const top = output.getBoundingClientRect().top + output.clientTop
  const row = Array.from(output.querySelectorAll<HTMLElement>('[data-log-id]')).find(node => node.getBoundingClientRect().bottom > top)
  return { row, offset: row ? row.getBoundingClientRect().top - top : 0, bottom: output.dataset.following === 'true', scrollTop: output.scrollTop }
}

type LogPosition = ReturnType<typeof visibleLogPosition>

export function LogsDetailTabs({ project, container, title, status, children }: { project: Project; container?: string; title: string; status: ReactNode; children: ReactNode }) {
  const [tab, setTab] = useState('Overview')
  const [focused, setFocused] = useState(false)
  const enter = useRef<HTMLButtonElement>(null)
  const exit = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const position = useRef<LogPosition | undefined>(undefined)
  const clampedPosition = useRef<{ original: LogPosition; focused: LogPosition } | undefined>(undefined)
  const id = useId()
  const toggleFocus = (next: boolean) => {
    const output = panel.current?.querySelector<HTMLElement>('.logs-output')
    if (output) {
      const current = visibleLogPosition(output), clamped = clampedPosition.current
      position.current = !next && !current.bottom && clamped?.original.row?.isConnected && current.row === clamped.focused.row && current.offset === clamped.focused.offset && current.scrollTop === clamped.focused.scrollTop ? clamped.original : current
    }
    clampedPosition.current = undefined
    setFocused(next)
  }
  useLayoutEffect(() => {
    const saved = position.current
    if (!saved) return
    position.current = undefined
    const output = panel.current?.querySelector<HTMLElement>('.logs-output')
    if (output && saved.bottom) output.scrollTop = output.scrollHeight
    else if (output && saved.row?.isConnected) {
      const target = output.scrollTop + saved.row.getBoundingClientRect().top - output.getBoundingClientRect().top - output.clientTop - saved.offset
      output.scrollTop = target
      // A taller pane can clamp a paused position; keep the original only until the view moves.
      if (focused && target - output.scrollTop > 1) clampedPosition.current = { original: saved, focused: visibleLogPosition(output) }
    }
    const button = focused ? exit.current : enter.current
    button?.focus({ preventScroll: true })
  }, [focused])
  const keys = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
    event.preventDefault()
    const next = event.key === 'Home' ? 'Overview' : event.key === 'End' ? 'Logs' : tab === 'Logs' ? 'Overview' : 'Logs'
    setTab(next)
    event.currentTarget.querySelector<HTMLButtonElement>(`[data-tab="${next}"]`)?.focus()
  }
  return <><div className="detail-tabs-bar"><div className="detail-tabs" role="tablist" aria-label="Detail views" onKeyDown={keys}>{['Overview', 'Logs'].map(name => <button key={name} type="button" role="tab" data-tab={name} id={`${id}-${name}`} aria-controls={`${id}-panel`} aria-selected={tab === name} tabIndex={tab === name ? 0 : -1} onClick={() => setTab(name)}>{name}</button>)}</div>{tab === 'Logs' && <button ref={enter} type="button" className="logs-focus-button" onClick={() => toggleFocus(true)}><svg aria-hidden="true" viewBox="0 0 24 24"><path d="M4 9V4h5m6 0h5v5m0 6v5h-5m-6 0H4v-5" /></svg>Focus logs</button>}</div>
    <div ref={panel} id={`${id}-panel`} role={focused ? 'region' : 'tabpanel'} aria-labelledby={focused ? `${id}-focus` : `${id}-${tab}`} className={tab === 'Logs' ? `detail-logs-panel${focused ? ' logs-focused' : ''}` : undefined} onKeyDown={event => {
      if (!focused || event.key !== 'Escape' || event.defaultPrevented || (event.target instanceof Element && event.target.closest('input, select, textarea, [contenteditable]'))) return
      event.preventDefault()
      event.stopPropagation()
      toggleFocus(false)
    }}>
      {focused && <header className="logs-focus-header"><h1 id={`${id}-focus`}>{title} <span>/ Logs</span></h1>{status}<button ref={exit} type="button" className="logs-focus-button" onClick={() => toggleFocus(false)}><svg aria-hidden="true" viewBox="0 0 24 24"><path d="m14 5-7 7 7 7" /></svg>Exit focus</button></header>}
      {tab === 'Overview' ? children : <LogsPanel key={`${project.name}/${container || ''}`} project={project} container={container} focused={focused} />}
    </div></>
}

type View = { entries: LogEntry[]; loading: boolean; following: boolean; hasMore: boolean; error: string; warnings: NonNullable<LogPage['warnings']>; reset: boolean; recycled: boolean; cursorInvalid?: boolean }
const initialView: View = { entries: [], loading: true, following: true, hasMore: false, error: '', warnings: [], reset: false, recycled: false }
type Actions = { older: () => void; latest: () => void; scroll: () => void; retry: () => void }
function LogsPanel({ project, container, focused }: { project: Project; container?: string; focused: boolean }) {
  const [selected, setSelected] = useState(container || '')
  const select = useRef<HTMLSelectElement>(null)
  const restoreSelectFocus = useRef(false)
  useLayoutEffect(() => {
    if (restoreSelectFocus.current) select.current?.focus({ preventScroll: true })
    restoreSelectFocus.current = false
  }, [selected])
  const scope = container === undefined && <label>Container<select ref={select} aria-label="Container" value={selected} onChange={event => {
    restoreSelectFocus.current = event.currentTarget === document.activeElement
    setSelected(event.currentTarget.value)
  }}><option value="">All containers</option>{project.containers.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
  return <LogsOutput key={selected} project={project} container={selected || undefined} scope={scope} focused={focused} />
}

function LogsOutput({ project, container, scope, focused }: { project: Project; container?: string; scope: ReactNode; focused: boolean }) {
  const [view, setView] = useState<View>(initialView)
  const [query, setQuery] = useState('')
  const [stream, setStream] = useState('')
  const [source, setSource] = useState('')
  const [wrap, setWrap] = useState(true)
  const filtered = selectLogEntries(view.entries, { query, stream, source })
  const filtering = useRef(false)
  const changingPresentation = useRef(false)
  useLayoutEffect(() => {
    filtering.current = !!(query || stream || source)
    changingPresentation.current = true
    let second = 0
    const first = requestAnimationFrame(() => { second = requestAnimationFrame(() => { changingPresentation.current = false }) })
    return () => { cancelAnimationFrame(first); cancelAnimationFrame(second) }
  }, [query, stream, source, wrap, focused])
  const output = useRef<HTMLDivElement>(null)
  const actions = useRef<Actions>({ older() {}, latest() {}, scroll() {}, retry() {} })
  const pendingPosition = useRef<{ id?: string; top?: number; bottom?: boolean } | undefined>(undefined)
  useLayoutEffect(() => {
    const pending = pendingPosition.current, element = output.current
    if (!pending || !element) return
    if (pending.bottom) element.scrollTop = element.scrollHeight
    else if (pending.id) {
      const row = Array.from(element.querySelectorAll<HTMLElement>('[data-log-id]')).find(node => node.dataset.logId === pending.id)
      if (row) element.scrollTop += row.getBoundingClientRect().top - (pending.top || 0)
    }
    pendingPosition.current = undefined
  }, [view.entries])
  useEffect(() => {
    let state = initialView, cursor: string | undefined, after: string | undefined
    let disposed = false, busy = false, timer: ReturnType<typeof setTimeout> | undefined, request: AbortController | undefined
    let failedMode: 'recent' | 'older' | 'newer' = 'recent'
    const update = (patch: Partial<View>) => { state = { ...state, ...patch }; if (!disposed) setView(state) }
    const schedule = (immediate = false) => {
      clearTimeout(timer)
      if (!disposed && !document.hidden && state.following && !state.reset && !state.error) timer = setTimeout(() => void load(after ? 'newer' : 'recent'), immediate ? 0 : 2000)
    }
    const anchor = () => {
      const element = output.current
      if (!element) return
      const top = element.getBoundingClientRect().top
      const row = Array.from(element.querySelectorAll<HTMLElement>('[data-log-id]')).find(node => node.getBoundingClientRect().bottom > top)
      if (row) pendingPosition.current = { id: row.dataset.logId, top: row.getBoundingClientRect().top }
    }
    const load = async (mode: 'recent' | 'older' | 'newer') => {
      if (disposed || busy || document.hidden || (mode === 'older' && (!state.hasMore || !cursor))) return
      clearTimeout(timer); busy = true; failedMode = mode
      request = new AbortController()
      const controller = request
      update({ loading: true })
      try {
        const page = await fetchLogs({ project: project.name, container, ...(mode === 'older' ? { cursor } : mode === 'newer' ? { after } : {}) }, controller.signal)
        if (disposed || controller.signal.aborted) return
        if (page.warnings?.length) {
          const merged = mergeLogEntries(state.entries, page.entries, mode === 'older' ? 'older' : 'newer')
          if (mode === 'older') anchor()
          else if (state.following) pendingPosition.current = { bottom: true }
          update({ entries: merged.entries, warnings: page.warnings, following: false, loading: false, reset: state.reset || merged.trimmed, error: 'Some log sources are unavailable; retry to continue without skipping output' })
          return
        }
        if (mode === 'older' && page.hasMore && (!page.nextCursor || page.nextCursor === cursor)) throw new Error('Older cursor did not advance; retry or reload recent logs.')
        if (mode !== 'older' && page.hasNewer && (!page.nextAfter || page.nextAfter === after)) throw new Error('Live cursor did not advance; retry or reload recent logs.')
        const merged = mode === 'recent' ? { entries: page.entries, trimmed: false } : mergeLogEntries(state.entries, page.entries, mode)
        if (mode === 'newer' && merged.trimmed) {
          // Start a fresh, contiguous live window with the page's matching
          // history cursor. All intervening newer pages have been consumed.
          cursor = page.nextCursor
          after = page.nextAfter
          pendingPosition.current = { bottom: true }
          update({ entries: page.entries, hasMore: !!cursor, warnings: [], recycled: true, loading: false, error: after ? '' : 'Live continuation unavailable; Jump to latest explicitly reloads recent logs' })
          schedule(!!page.hasNewer)
          return
        }
        if (mode === 'older') anchor()
        else if (state.following) pendingPosition.current = { bottom: true }
        if (mode !== 'newer') cursor = page.nextCursor
        if (mode !== 'older') after = page.nextAfter
        update({ entries: merged.entries, error: mode !== 'older' && !after ? 'Live continuation unavailable; Jump to latest explicitly reloads recent logs' : '', warnings: page.warnings || [], loading: false, hasMore: mode === 'newer' ? state.hasMore : page.hasMore, reset: mode === 'recent' ? false : state.reset || merged.trimmed, cursorInvalid: mode === 'recent' ? false : state.cursorInvalid })
        schedule(mode !== 'older' && !!page.hasNewer)
      } catch (reason) {
        if (!disposed && !controller.signal.aborted) update({ error: reason instanceof Error ? reason.message : String(reason), loading: false, ...(reason instanceof LogCursorError ? { reset: true, cursorInvalid: true, following: false } : {}) })
      } finally { if (request === controller) { busy = false; request = undefined } }
    }
    const cancel = () => { clearTimeout(timer); request?.abort(); request = undefined; busy = false; update({ loading: false }) }
    actions.current = {
      older: () => { update({ following: false }); void load('older') },
      latest: () => {
        cancel(); update({ following: true, error: '' }); pendingPosition.current = { bottom: true }
        if (output.current) output.current.scrollTop = output.current.scrollHeight
        void load(state.reset || !after ? 'recent' : 'newer')
      },
      retry: () => { update({ error: '' }); void load(failedMode) },
      scroll: () => {
        if (changingPresentation.current) return
        const element = output.current
        if (!element) return
        const atBottom = element.scrollHeight - element.scrollTop - element.clientHeight < 24
        if (!atBottom && state.following) { clearTimeout(timer); update({ following: false }) }
        else if (atBottom && !state.following && !state.reset && !state.error) { update({ following: true }); schedule(true) }
        if (!filtering.current && !atBottom && element.scrollTop < 100 && element.querySelector('[data-log-id]')) void load('older')
      },
    }
    const visibility = () => { if (document.hidden) cancel(); else schedule(true) }
    document.addEventListener('visibilitychange', visibility)
    void load('recent')
    return () => { disposed = true; clearTimeout(timer); request?.abort(); document.removeEventListener('visibilitychange', visibility) }
  }, [project.name, container])
  const download = () => {
    const url = URL.createObjectURL(new Blob([logDownload(filtered)], { type: 'text/plain;charset=utf-8' }))
    const link = document.createElement('a')
    link.href = url; link.download = `${project.name}-loaded-logs.txt`; link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return <section className="logs-view" aria-label="Container logs">
    <div className="logs-filters">
      {scope}
      <label className="logs-search">Search loaded logs<input type="search" aria-label="Search loaded logs" placeholder="Search loaded logs" value={query} onChange={event => setQuery(event.target.value)} /></label>
      <label>Stream (loaded)<select aria-label="Stream (loaded)" value={stream} onChange={event => setStream(event.target.value)}><option value="">All streams</option>{['stdout', 'stderr', 'tty'].map(value => <option key={value}>{value}</option>)}</select></label>
      <label>Source (loaded)<select aria-label="Source (loaded)" value={source} onChange={event => setSource(event.target.value)}><option value="">All sources</option>{project.containers.filter(item => !container || item.id === container).map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
      <label className="logs-wrap"><input type="checkbox" checked={wrap} onChange={event => setWrap(event.target.checked)} />Wrap lines</label>
    </div>
    <div className="logs-controls"><button type="button" disabled={!view.hasMore || view.loading} onClick={() => actions.current.older()}>Load older</button><span role="status">{view.loading ? 'Loading…' : view.following ? 'Following' : 'Paused'}</span><button type="button" onClick={() => actions.current.latest()}>Jump to latest</button><button type="button" aria-label="Download loaded filtered logs" disabled={!filtered.length} onClick={download}>Download</button><span className="logs-count">{filtered.length} shown · {view.entries.length} loaded</span></div>
    {view.reset && !view.cursorInvalid && <p className="logs-warning">Loaded log window reached its limit. Newest output is paused; keep loading older history, or Jump to latest to reload recent logs.</p>}
    {view.cursorInvalid && <p className="logs-warning">Log history changed or the cursor expired. Jump to latest explicitly reloads recent logs.</p>}
    {view.recycled && !view.reset && <p className="logs-buffer-note">Earlier loaded output was released to keep the live buffer bounded. Scroll up to load retained history again.</p>}
    {view.error && <p role="alert" className="logs-warning">Logs unavailable: {view.error}. Existing output retained. <button type="button" onClick={() => actions.current.retry()}>Retry logs</button></p>}
    {view.warnings.length > 0 && <details className="logs-warning" open><summary>Partial logs · {view.warnings.length} source warnings</summary>{view.warnings.map((warning, index) => <p key={`${warning.containerId}-${index}`}>{warning.container}: {warning.message}</p>)}</details>}
    <div ref={output} className={`logs-output${wrap ? ' wrap' : ''}`} data-following={view.following} role="region" aria-label="Log output" tabIndex={0} onScroll={() => actions.current.scroll()}>{filtered.map(entry => <div className="log-line" data-log-id={entry.id} key={entry.id}><time dateTime={entry.timestamp} title={entry.timestamp}>{entry.timestamp}</time><span className="log-source">{entry.container}</span><span className={`log-stream ${entry.stream}`}>{entry.stream}</span><span className="log-message">{entry.message}</span></div>)}{!filtered.length && !view.loading && !view.error && <p>{view.entries.length ? 'No matching loaded logs. Filters only search loaded lines; Load older to inspect more history.' : 'No logs reported.'}</p>}</div>
  </section>
}
