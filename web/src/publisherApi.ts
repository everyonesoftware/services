import { AuthError, sessionExpiredEvent, sessionFetch } from './authApi.ts'

export type Repository = { id: number; fullName: string; defaultBranch: string; installationId: number }
export type Tailnet = { clientId: string; audience: string; tags: string }
export type TailnetGuidance = { issuer: string; subject: string; subjectError?: string; tailnet: Tailnet }
export type Publication = { version?: string; digest: string; tags: string[]; commit: string; runUrl: string; publishedAt: string; receivedAt: string; result: string }
export type Connection = {
  id: string; repository: Repository; imageRepository: string
  state: 'configured' | 'awaiting_callback' | 'verified' | 'error' | 'disconnected'
  error?: string; keyId?: string; createdAt: string; verifiedAt?: string
  remoteDisablePending?: boolean
  lastPublication?: Publication
}
export type PublisherStatus = {
  available: boolean; reason?: string
  github: { connected: boolean; name?: string; installUrl?: string; permissionsChecked?: boolean; variablesWrite?: boolean; settingsUrl?: string }
  tailnet: Tailnet
  tailnets?: Record<string, Tailnet>
  endpoint: string; connections: Connection[]
}
export type PublisherPlan = {
  supported: boolean; problems: string[]; repository: Repository; imageRepository: string
  approval?: string
  variables: Record<string, string>; secretName: 'SERVICES_PUBLISH_TOKEN'
  workflow?: { supported: boolean; problems: string[]; files: { path: string; before: string; after: string }[]; fingerprint: string }
}
export type GitHubStart = { action: string; manifest: string }

export function githubVariablesReady(github: PublisherStatus['github']) {
  return github.connected === true && github.permissionsChecked === true && github.variablesWrite === true
}
export function readyForPublisherRepositories(status: PublisherStatus | null) {
  return !!(status?.available === true && githubVariablesReady(status.github))
}

export function imageRepositoryProblem(value: string) {
  return value.length <= 255 && /^ghcr\.io\/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:\/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)+$/.test(value)
    ? '' : 'Enter an exact GHCR image repository, such as ghcr.io/acme/site; no tags, digests, URLs, spaces or wildcards.'
}
export function canApplyPlan(plan: PublisherPlan | null, repository: Repository | null, imageRepository: string) {
  return !!(plan?.supported && !plan.problems.length && !plan.workflow?.files.length && matchesPlan(plan, repository, imageRepository))
}
function matchesPlan(plan: PublisherPlan, repository: Repository | null, imageRepository: string) {
  return !!(repository && !imageRepositoryProblem(imageRepository)
    && plan.imageRepository === imageRepository && plan.secretName === 'SERVICES_PUBLISH_TOKEN'
    && plan.repository.id === repository.id && plan.repository.fullName === repository.fullName
    && plan.repository.installationId === repository.installationId && plan.repository.defaultBranch === repository.defaultBranch)
}
export function awaitingCallback(connections: Connection[]) { return connections.some(connection => connection.state === 'awaiting_callback') }
export function canOpenWorkflowPlan(plan: PublisherPlan | null, repository: Repository | null, imageRepository: string) {
  return !!(plan?.workflow?.supported && !plan.workflow.problems.length && plan.workflow.fingerprint && plan.workflow.files.length && matchesPlan(plan, repository, imageRepository))
}
function githubUrl(value: string) {
  try { const url = new URL(value); return url.protocol === 'https:' && url.hostname === 'github.com' && !url.port && !url.username && !url.password && !url.hash ? url : null } catch { return null }
}
export function githubPermissionsUrl(value: string) {
  // Match the original string, not a normalized URL: reject traversal, encodings,
  // explicit ports (including :443), credentials and even empty query/hash suffixes.
  return /^https:\/\/github\.com\/(?:settings\/apps|organizations\/[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?\/settings\/apps)\/[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?\/permissions$/.test(value) && !/[\r\n]/.test(value)
}
export function githubManifestAction(value: string) {
  const url = githubUrl(value)
  return !!(url && (url.pathname === '/settings/apps/new' || /^\/organizations\/[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?\/settings\/apps\/new$/.test(url.pathname)) && url.searchParams.get('state'))
}
export function githubInstallUrl(value: string) { return !!githubUrl(value)?.pathname.match(/^\/apps\/[^/]+\/installations\/(new|select_target)\/?$/) }
function repositoryGitHubUrl(value: string, repository: Repository, kind: 'pull' | 'actions/runs') {
  const url = githubUrl(value)
  if (!url || url.search) return false
  const prefix = `/${repository.fullName}/${kind}/`.toLowerCase()
  return url.pathname.toLowerCase().startsWith(prefix) && /^[1-9][0-9]*$/.test(url.pathname.slice(prefix.length))
}
export function githubPullRequestUrl(value: string, repository: Repository) { return repositoryGitHubUrl(value, repository, 'pull') }
export function githubRunUrl(value: string, repository: Repository) { return repositoryGitHubUrl(value, repository, 'actions/runs') }

export class PublisherRequestTimeout extends Error {
  readonly mutation: boolean
  stateRefreshed = false
  constructor(method: string) {
    const mutation = method !== 'GET'
    super(mutation ? 'Request timed out. The server may have applied this change. Refresh saved state before retrying.' : 'Request timed out. Refresh saved state to try again.')
    this.name = 'PublisherRequestTimeout'
    this.mutation = mutation
  }
}

export function createPublisherApi(fetcher: typeof fetch = (...args) => fetch(...args), options: { timeoutMs?: number } = {}) {
  const timeoutMs = options.timeoutMs ?? 125_000
  if (!Number.isFinite(timeoutMs) || timeoutMs < 1 || timeoutMs > 125_000) throw new Error('Invalid publisher request timeout')
  const request = async <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    const controller = new AbortController()
    const cancelled = () => controller.abort(new DOMException('Request cancelled', 'AbortError'))
    let rejectAbort: (reason: unknown) => void = () => {}
    const aborted = new Promise<never>((_resolve, reject) => { rejectAbort = reject })
    const abort = () => rejectAbort(controller.signal.reason)
    controller.signal.addEventListener('abort', abort, { once: true })
    signal?.addEventListener('abort', cancelled, { once: true })
    if (signal?.aborted) cancelled()
    const timer = setTimeout(() => controller.abort(new PublisherRequestTimeout(method)), timeoutMs)
    try {
      if (controller.signal.aborted) return await aborted
      const operation = async (): Promise<T> => {
        const authenticated = typeof window === 'undefined' ? fetcher : sessionFetch(fetcher, () => window.dispatchEvent(new Event(sessionExpiredEvent)), window.location.origin)
        const response = await authenticated(`/api/publishers${path}`, {
          method, headers: { 'Content-Type': 'application/json' }, credentials: 'same-origin', cache: 'no-store', signal: controller.signal,
          body: body === undefined ? undefined : JSON.stringify(body),
        })
        if (!response.ok) {
          const text = (await response.text()).trim()
          let explanation = text
          try { const data = JSON.parse(text); explanation = data.error || data.message || text } catch { /* Plain text server failures. */ }
          throw new AuthError(explanation || `Request failed (${response.status})`, response.status)
        }
        return await response.json() as T
      }
      return await Promise.race([operation(), aborted])
    } finally {
      clearTimeout(timer)
      signal?.removeEventListener('abort', cancelled)
      controller.signal.removeEventListener('abort', abort)
    }
  }
  return {
    status: (signal?: AbortSignal) => request<PublisherStatus>('', 'GET', undefined, signal),
    checkGitHubPermissions: () => request<PublisherStatus>('/github/permissions', 'POST', {}),
    startGitHub: (organization?: string) => request<GitHubStart>('/github/start', 'POST', organization === undefined ? {} : { organization }),
    saveTailnet: (body: Tailnet & { repository: Repository }) => request<PublisherStatus>('/tailnet', 'PUT', body),
    tailnetGuidance: (repository: Repository) => request<TailnetGuidance>('/tailnet/guidance', 'POST', { repository }),
    repositories: () => request<{ repositories: Repository[] }>('/repositories'),
    plan: (repository: Repository, imageRepository: string) => request<PublisherPlan>('/plan', 'POST', { repository, imageRepository }),
    apply: (repository: Repository, imageRepository: string, approval?: string) => request<{ connection: Connection }>('', 'POST', { repository, imageRepository, ...(approval === undefined ? {} : { approval }) }),
    test: (id: string) => request<{ connection: Connection }>(`/${encodeURIComponent(id)}/test`, 'POST', {}),
    workflow: (repository: Repository, imageRepository: string, fingerprint: string) => request<{ prUrl: string }>('/workflow', 'POST', { repository, imageRepository, fingerprint }),
    disconnect: (id: string) => request<{ connection: Connection }>(`/${encodeURIComponent(id)}/disconnect`, 'POST', {}),
    rotate: (id: string) => request<{ connection: Connection }>(`/${encodeURIComponent(id)}/rotate`, 'POST', {}),
    recover: (id: string) => request<{ connection: Connection }>(`/${encodeURIComponent(id)}/recover`, 'POST', {}),
  }
}

// A rejected apply can still have persisted a connection. Read local saved state,
// but preserve the actionable apply error if the follow-up read also fails.
async function readSavedPublisherStatus(client: Pick<ReturnType<typeof createPublisherApi>, 'status'>) {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 12_000)
  try { return await client.status(controller.signal) }
  finally { clearTimeout(timer) }
}

export async function checkGitHubPermissionsAndRefresh(client: Pick<ReturnType<typeof createPublisherApi>, 'checkGitHubPermissions'>, current: PublisherStatus, onStatus: (status: PublisherStatus) => void, clearReview: () => void) {
  // A failed recheck must not leave the previous grant or review usable.
  onStatus({ ...current, github: { ...current.github, permissionsChecked: false, variablesWrite: false } })
  clearReview()
  onStatus(await client.checkGitHubPermissions())
}

export async function applyAndRefresh(client: Pick<ReturnType<typeof createPublisherApi>, 'apply' | 'status'>, plan: Pick<PublisherPlan, 'repository' | 'imageRepository' | 'approval'>, onStatus: (status: PublisherStatus) => void) {
  let result: { connection: Connection }
  try { result = await client.apply(plan.repository, plan.imageRepository, plan.approval) }
  catch (cause) {
    try { onStatus(await readSavedPublisherStatus(client)); if (cause instanceof PublisherRequestTimeout) cause.stateRefreshed = true } catch { /* The original mutation failure remains visible. */ }
    throw cause
  }
  onStatus(await readSavedPublisherStatus(client))
  return result
}

export async function refreshAfterPublisherTimeout(cause: unknown, client: Pick<ReturnType<typeof createPublisherApi>, 'status'>, onStatus: (status: PublisherStatus) => void) {
  if (!(cause instanceof PublisherRequestTimeout) || !cause.mutation || cause.stateRefreshed) return
  try { onStatus(await readSavedPublisherStatus(client)); cause.stateRefreshed = true } catch { /* Preserve the uncertain outcome; do not infer a rollback or success. */ }
}
