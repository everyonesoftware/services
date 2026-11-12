const buildPattern = /^[a-f0-9]{20}$/
type CheckOptions = { timeoutMs?: number; signal?: AbortSignal }
const reloadKey = 'services-web-reload'

export function rememberWebReload(storage: Pick<Storage, 'setItem'>, target: string) {
  try { if (buildPattern.test(target)) storage.setItem(reloadKey, target) } catch { /* Storage is optional. */ }
}

export function webReloadCompleted(storage: Pick<Storage, 'getItem'>, current: string): boolean {
  try { return buildPattern.test(current) && storage.getItem(reloadKey) === current } catch { return false }
}

export function consumeWebReload(storage: Pick<Storage, 'getItem' | 'removeItem'>, current: string): boolean {
  try {
    const completed = webReloadCompleted(storage, current)
    storage.removeItem(reloadKey)
    return completed
  } catch { return false }
}

export function currentWebBuild(page: Pick<Document, 'querySelector'>): string | null {
  const id = page.querySelector('meta[name="services-build"]')?.getAttribute('content')
  return id && buildPattern.test(id) ? id : null
}

export async function readWebBuild(fetcher: typeof fetch = fetch, { timeoutMs = 10000, signal }: CheckOptions = {}): Promise<string> {
  if (signal?.aborted) throw new Error('Update check cancelled.')
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  let cancel = () => {}
  const interrupted = new Promise<never>((_, reject) => {
    cancel = () => { controller.abort(); reject(new Error('Update check cancelled.')) }
    signal?.addEventListener('abort', cancel, { once: true })
    timer = setTimeout(() => { controller.abort(); reject(new Error('Update check timed out. Please try again.')) }, timeoutMs)
  })
  try {
    const request = async () => {
      const response = await fetcher('/web-build.json', { method: 'GET', cache: 'no-store', credentials: 'same-origin', signal: controller.signal })
      if (!response.ok) throw new Error(`Couldn’t check for updates (HTTP ${response.status}). Please try again.`)
      let value: unknown
      try { value = await response.json() } catch { throw new Error('Invalid web build response. Please try again.') }
      if (!value || typeof value !== 'object' || !('id' in value) || typeof value.id !== 'string' || !buildPattern.test(value.id)) {
        throw new Error('Invalid web build identity. Please try again.')
      }
      return value.id
    }
    return await Promise.race([interrupted, request()])
  } finally {
    clearTimeout(timer)
    signal?.removeEventListener('abort', cancel)
  }
}
