import type { UpdateEvent } from './types.ts'

export async function checkForUpdates(fetcher: typeof fetch = fetch): Promise<void> {
  const response = await fetcher('/api/updates/check', { method: 'POST' })
  if (!response.ok) throw new Error((await response.text()).trim() || `Image check failed (${response.status})`)
}

export async function updateProject(name: string, onEvent: (event: UpdateEvent) => void, fetcher: typeof fetch = fetch, timeoutMs = 16 * 60_000): Promise<void> {
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<never>((_resolve, reject) => {
    timer = setTimeout(() => {
      reject(new Error('Update timed out; result is unconfirmed. The server may still be working. Verify service state before retrying.'))
      controller.abort()
    }, timeoutMs)
  })
  const operation = async () => {
    const response = await fetcher(`/api/projects/${encodeURIComponent(name)}/update`, { method: 'POST', signal: controller.signal })
    await consumeUpdateStream(response, onEvent, controller.signal)
  }
  try { await Promise.race([operation(), timeout]) }
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
    if (!event || !['progress', 'complete', 'error'].includes(event.type) || typeof event.message !== 'string') throw new Error('Invalid update event received')
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
