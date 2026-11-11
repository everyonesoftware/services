import type { Icon, IconList, IconTarget, Operation, ResolutionDecision, UpdateEvent } from './types.ts'

async function resolutionRequest<T>(path: string, body: unknown, read: (response: Response) => Promise<T>, fetcher: typeof fetch, timeoutMs: number): Promise<T> {
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<never>((_resolve, reject) => {
    timer = setTimeout(() => {
      reject(new Error('Request timed out; the outcome is unconfirmed. Refresh monitoring before retrying.'))
      controller.abort()
    }, timeoutMs)
  })
  const request = async () => {
    const response = await fetcher(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: controller.signal })
    if (!response.ok) throw new Error((await response.text()).trim() || `Resolution request failed (${response.status})`)
    return await read(response)
  }
  try { return await Promise.race([request(), timeout]) }
  finally { clearTimeout(timer) }
}

export function resolveIncidents(decisions: ResolutionDecision[], fetcher: typeof fetch = fetch, timeoutMs = 15_000): Promise<{ undoToken: string }> {
  return resolutionRequest('/api/monitor/resolve', { decisions }, async response => {
    const result = await response.json() as { undoToken: string }
    if (!result || typeof result.undoToken !== 'string' || !result.undoToken) throw new Error('Missing Undo token; the outcome is unconfirmed. Refresh monitoring before retrying.')
    return result
  }, fetcher, timeoutMs)
}

export function undoResolution(token: string, fetcher: typeof fetch = fetch, timeoutMs = 15_000): Promise<void> {
  return resolutionRequest('/api/monitor/undo', { token }, async () => {}, fetcher, timeoutMs)
}

async function iconRequest<T>(url: string, options: RequestInit, fetcher: typeof fetch, timeoutMs: number): Promise<T> {
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<never>((_resolve, reject) => {
    timer = setTimeout(() => {
      reject(new Error(options.method === 'GET' ? 'Icon request timed out.' : 'Icon request timed out; the outcome is unconfirmed. Refresh before retrying.'))
      controller.abort()
    }, timeoutMs)
  })
  const request = async () => {
    const response = await fetcher(url, { ...options, signal: controller.signal })
    if (!response.ok) throw new Error((await response.text()).trim() || `Icon request failed (${response.status})`)
    return response.status === 204 ? undefined as T : await response.json() as T
  }
  try { return await Promise.race([request(), timeout]) }
  finally { clearTimeout(timer) }
}

export function getIcons(fetcher: typeof fetch = fetch, timeoutMs = 12_000): Promise<IconList> {
  return iconRequest('/api/icons', { method: 'GET', cache: 'no-store' }, fetcher, timeoutMs)
}

export function saveIcon(target: IconTarget, url: string, fetcher: typeof fetch = fetch, timeoutMs = 30_000): Promise<Icon> {
  return iconRequest('/api/icons', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ kind: target.kind, key: target.key, url }) }, fetcher, timeoutMs)
}

export function uploadIcon(target: IconTarget, file: File, fetcher: typeof fetch = fetch, timeoutMs = 30_000): Promise<Icon> {
  return iconRequest(`/api/icons/upload?kind=${encodeURIComponent(target.kind)}&key=${encodeURIComponent(target.key)}`, { method: 'PUT', headers: { 'Content-Type': file.type || 'application/octet-stream' }, body: file }, fetcher, timeoutMs)
}

export function deleteIcon(target: IconTarget, fetcher: typeof fetch = fetch, timeoutMs = 12_000): Promise<void> {
  return iconRequest(`/api/icons?kind=${encodeURIComponent(target.kind)}&key=${encodeURIComponent(target.key)}`, { method: 'DELETE' }, fetcher, timeoutMs)
}

export async function checkForUpdates(fetcher: typeof fetch = fetch): Promise<void> {
  const response = await fetcher('/api/updates/check', { method: 'POST' })
  if (!response.ok) throw new Error((await response.text()).trim() || `Image check failed (${response.status})`)
}

export async function acknowledgeOperation(id: string, fetcher: typeof fetch = fetch): Promise<void> {
  const response = await fetcher(`/api/operations/${encodeURIComponent(id)}/acknowledge`, { method: 'POST', signal: AbortSignal.timeout(15_000) })
  if (!response.ok) throw new Error((await response.text()).trim() || `Acknowledgement failed (${response.status})`)
}

export async function updateProject(name: string, fetcher: typeof fetch = fetch, timeoutMs = 12_000): Promise<Operation> {
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<never>((_resolve, reject) => {
    timer = setTimeout(() => {
      reject(new Error('Submission timed out; acceptance is unconfirmed. Check Docker activity before retrying. The server may still be working.'))
      controller.abort()
    }, timeoutMs)
  })
  const operation = async () => {
    const response = await fetcher(`/api/projects/${encodeURIComponent(name)}/update`, { method: 'POST', signal: controller.signal })
    if (!response.ok) throw new Error((await response.text()).trim() || `Request failed (${response.status})`)
    const result = await response.json() as Operation
    if (!result || typeof result.id !== 'string' || typeof result.project !== 'string' || !['queued', 'running', 'complete', 'error'].includes(result.state) || !Array.isArray(result.messages)) throw new Error('Invalid operation received; check Docker activity before retrying.')
    return result
  }
  try { return await Promise.race([operation(), timeout]) }
  finally { clearTimeout(timer) }
}

export async function consumeUpdateStream(response: Response, onEvent: (event: UpdateEvent) => void, signal?: AbortSignal): Promise<void> {
  if (!response.ok) throw new Error((await response.text()).trim() || `Request failed (${response.status})`)
  const reader = response.body?.getReader()
  if (!reader) throw new Error('Progress stream unavailable')
  const abort = () => { void reader.cancel().catch(() => {}) }
  signal?.addEventListener('abort', abort, { once: true })
  if (signal?.aborted) abort()
  const decoder = new TextDecoder()
  let buffer = ''
  let complete = false
  const accept = (line: string) => {
    if (!line.trim()) return
    let event: UpdateEvent
    try { event = JSON.parse(line) as UpdateEvent } catch { throw new Error('Invalid update event received') }
    if (!event || !['accepted', 'progress', 'complete', 'error'].includes(event.type) || typeof event.message !== 'string') throw new Error('Invalid update event received')
    onEvent(event)
    if (event.type === 'error') throw new Error(event.message || 'Update failed')
    if (event.type === 'complete') complete = true
  }
  try {
    while (true) {
      const { value, done } = await reader.read()
      buffer += decoder.decode(value, { stream: !done })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      lines.forEach(accept)
      if (done) { accept(buffer); break }
    }
    if (!complete) throw new Error('Update stream ended before completion. Verify the service state before retrying.')
  } finally { signal?.removeEventListener('abort', abort); reader.releaseLock() }
}
