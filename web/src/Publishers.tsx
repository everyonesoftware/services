import { useEffect, useRef, useState } from 'react'
import type { FormEvent, RefObject } from 'react'
import { applyAndRefresh, awaitingCallback, canApplyPlan, canOpenWorkflowPlan, createPublisherApi, githubInstallUrl, githubManifestAction, githubPullRequestUrl, githubRunUrl, imageRepositoryProblem, refreshAfterPublisherTimeout } from './publisherApi.ts'
import type { Connection, GitHubStart, PublisherPlan, PublisherStatus, Repository } from './publisherApi.ts'
import type { AuthUser } from './authApi.ts'
import './Publishers.css'

export type AppPage = 'monitor' | 'publishers' | 'accounts'
type NavigationProps = { active: AppPage; onMonitor?: () => void; onPublishers?: () => void; onAccounts?: () => void; disabled?: boolean }
// Kept in this module so all three shells use one item order and active-page rule.
export function AppNavigation({ active, onMonitor, onPublishers, onAccounts, disabled = false }: NavigationProps) {
  const items = [['monitor', 'Monitor', onMonitor], ['publishers', 'Publishers', onPublishers], ['accounts', 'Accounts', onAccounts]] as const
  return <nav className="app-navigation" aria-label="Main navigation">{items.map(([page, label, action]) => <a key={page} href={`#${page}`} aria-current={active === page ? 'page' : undefined} aria-disabled={disabled || undefined} onClick={event => {
    if (disabled || action) event.preventDefault()
    if (!disabled) action?.()
  }}>{label}</a>)}</nav>
}

const api = createPublisherApi()
const errorMessage = (cause: unknown) => cause instanceof Error ? cause.message : 'Request failed. Please try again.'
const dateLabel = (value: string) => Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : 'Not available'
const steps = ['Connections', 'Repository', 'Review setup', 'Verify push']

export function GitHubManifestForm({ manifest, lockRef }: { manifest: GitHubStart; lockRef: RefObject<boolean> }) {
  return <form action={manifest.action} method="post" onSubmit={event => {
    if (lockRef.current) { event.preventDefault(); return }
    // Do not set busy here: disabling the enclosing fieldset before the browser
    // serializes this native POST omits the manifest, so GitHub receives no JSON.
    lockRef.current = true
  }}><input type="hidden" name="manifest" value={manifest.manifest} /><button className="button primary" type="submit">Continue to GitHub ↗</button></form>
}

type RegistrationProps = { owner: 'personal' | 'organization'; organization: string; onOwner: (owner: 'personal' | 'organization') => void; onOrganization: (value: string) => void; onSubmit: (event: FormEvent<HTMLFormElement>) => void }
export function GitHubRegistration({ owner, organization, onOwner, onOrganization, onSubmit }: RegistrationProps) {
  return <form className="publisher-registration" onSubmit={onSubmit}><label>Register GitHub App under<select value={owner} onChange={event => onOwner(event.target.value as RegistrationProps['owner'])}><option value="personal">Personal account</option><option value="organization">Organization</option></select></label>
    {owner === 'organization' && <><label>Organization name<input name="organization" required value={organization} onChange={event => onOrganization(event.target.value)} pattern="[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?" maxLength={39} placeholder="Your GitHub organization" autoComplete="off" spellCheck={false} /></label><p className="publisher-muted">Organization owner approval is required. To install on an organization’s repositories, a private App must be registered to that organization—not your personal account.</p></>}
    <button className="button primary" type="submit">Prepare GitHub consent ↗</button></form>
}

export function PublisherReview({ plan }: { plan: PublisherPlan }) {
  const workflowReady = canOpenWorkflowPlan(plan, plan.repository, plan.imageRepository)
  return <><h2>Review publisher setup</h2><p>{plan.repository.fullName} · <code>{plan.imageRepository}</code></p>
    {workflowReady ? <div className="publisher-info publisher-warning"><strong>Workflow changes need a setup pull request</strong>Review the exact files below. Open setup PR only creates the proposed workflow change; no publisher key is created until you merge and review again.</div> : (!plan.supported || plan.problems.length > 0 || !!plan.workflow?.files.length) && <div className="publisher-info publisher-warning" role="alert"><strong>Setup cannot be applied</strong><ul>{[...(plan.problems.length ? plan.problems : ['This repository does not have a supported existing notification workflow.']), ...(plan.workflow?.problems ?? [])].map((problem, index) => <li key={index}>{problem}</li>)}</ul><p>Automatic setup is not supported for this plan. Add or repair the required workflow manually, then return to the repository step and review again.</p><a href="https://github.com/everyonesoftware/services#push-publisher-setup" target="_blank" rel="noreferrer">Publisher setup documentation ↗</a></div>}
    {workflowReady && <section className="publisher-workflow-files" aria-label="Proposed workflow diff">{plan.workflow!.files.map(file => <section className="publisher-workflow-file" key={file.path}><h3><code>{file.path}</code></h3><div className="publisher-diff"><section><h4>Before</h4><pre>{file.before || '(File does not exist)'}</pre></section><section><h4>After</h4><pre>{file.after || '(File removed)'}</pre></section></div></section>)}</section>}
    <div className="publisher-review"><section className="publisher-config" aria-label="Exact repository configuration"><h3>Actions variables · desired values</h3><dl>{Object.entries(plan.variables).sort(([left], [right]) => left.localeCompare(right)).map(([name, value]) => <div key={name}><dt><code>{name}</code></dt><dd><code>{value}</code></dd></div>)}</dl>{!Object.keys(plan.variables).length && <p>No variables proposed.</p>}</section>
      <section className="publisher-resources"><h3>Only on your approval</h3><ul><li>A dedicated <code>publications:write</code> key restricted to <code>{plan.imageRepository}</code>.</li><li>One encrypted Actions secret: <code>{plan.secretName}</code>. Its value is transferred server-to-server, never shown here.</li><li>Missing configuration is created. Existing values are not overwritten; conflicts stop setup.</li><li>A fresh, single-use push test. No image is built or published by this test.</li></ul></section></div>
    <div className="publisher-info"><strong>{workflowReady ? 'Separate approval, then verification' : 'Existing supported workflows only'}</strong>{workflowReady ? 'Workflow edits are proposed only through the setup PR. Merge it, then request a fresh review before applying configuration.' : 'No workflow files are changed by Apply. The repository must already have the supported publication hook and test-dispatch workflow.'} Notification activation requires an authenticated callback; an accepted dispatch is not verification.</div>
  </>
}

export function PublisherWorkflowPending({ plan, prUrl }: { plan: PublisherPlan; prUrl: string }) {
  return <><h2>Waiting for workflow approval</h2><p>{plan.repository.fullName} · <code>{plan.imageRepository}</code></p><div className="publisher-info publisher-warning"><strong>Merge the setup PR</strong>Review and merge the proposed changes on GitHub. Services does not bypass repository rules or check merge status in the background. Then select Review again to inspect the current workflow before applying configuration.</div>{githubPullRequestUrl(prUrl, plan.repository) ? <a className="button secondary publisher-pr-link" href={prUrl} target="_blank" rel="noreferrer">Review setup pull request ↗</a> : <p className="notice danger" role="alert">The setup PR link could not be verified. No external link was opened.</p>}</>
}

type ManagementAction = 'disconnect' | 'rotate'
type ConnectionProps = { connection: Connection; busy: boolean; onTest: (id: string) => void; onRecover?: (id: string) => void; onManage?: (connection: Connection, action: ManagementAction) => void }
export function PublisherConnection({ connection, busy, onTest, onRecover, onManage }: ConnectionProps) {
  const labels: Record<Connection['state'], string> = { configured: 'Configured', awaiting_callback: 'Waiting for authenticated callback', verified: 'Connectivity verified', error: 'Connection needs attention', disconnected: 'Disconnected' }
  const publication = connection.state === 'verified' && connection.lastPublication?.digest && connection.lastPublication.receivedAt ? connection.lastPublication : null
  return <article className="publisher-connection" aria-label={`Publisher ${connection.repository.fullName}`}><div className="publisher-connection-head"><div><p className="eyebrow">GITHUB ACTIONS PUBLISHER</p><h2>{connection.repository.fullName}</h2><p><code>{connection.imageRepository}</code></p></div><span className={`publisher-badge ${connection.state === 'verified' ? 'good' : ''}`}>{labels[connection.state]}</span></div>
    {connection.state === 'verified' ? <div className="publisher-info"><strong>The push connection is ready</strong>A GitHub runner completed the authenticated connectivity test—not a successful publication. Real image announcements are separate; installation still requires your confirmation.</div> : connection.state === 'awaiting_callback' ? <div className="publisher-info" role="status"><strong>Waiting for authenticated callback</strong>GitHub runner → private Tailscale route → Services. This page refreshes this server’s saved acknowledgement every 3 seconds while waiting. It does not poll GitHub or check for publications.</div> : <p className="publisher-muted">{connection.state === 'disconnected' ? 'This connection is disconnected.' : 'Configuration is saved. Push delivery has not been verified.'}</p>}
    {connection.error && <p className="notice danger" role="alert">{connection.error}</p>}
    {publication && <section className="publisher-publication" aria-label="Latest publication"><h3>Latest publication</h3><p className="publisher-muted">Real image metadata received by push. No image is pulled or installed by this announcement.</p><dl className="publisher-facts">{publication.version && <div><dt>Version</dt><dd>{publication.version}</dd></div>}<div><dt>Tags</dt><dd><code>{publication.tags.join(' · ') || 'No tags supplied'}</code></dd></div><div className="publisher-digest"><dt>Image digest</dt><dd><code>{publication.digest}</code></dd></div><div><dt>Commit</dt><dd><code>{publication.commit || 'Not supplied'}</code></dd></div><div><dt>Delivery result</dt><dd>{publication.result}</dd></div><div><dt>Published</dt><dd>{dateLabel(publication.publishedAt)}</dd></div><div><dt>Received</dt><dd>{dateLabel(publication.receivedAt)}</dd></div></dl>{githubRunUrl(publication.runUrl, connection.repository) && <a href={publication.runUrl} target="_blank" rel="noreferrer">View publishing workflow run ↗</a>}</section>}
    {connection.state === 'error' && <p className="publisher-muted">Check the workflow run in GitHub, the federation’s repository/ref trust, and the runner tag’s access to this server’s HTTPS endpoint. Repair reuses the saved setup; no duplicate key is created.</p>}
    <dl className="publisher-facts"><div><dt>Default branch</dt><dd><code>{connection.repository.defaultBranch}</code></dd></div><div><dt>Created</dt><dd>{dateLabel(connection.createdAt)}</dd></div>{connection.verifiedAt && <div><dt>Connectivity verified</dt><dd>{dateLabel(connection.verifiedAt)}</dd></div>}</dl>
    {connection.state !== 'disconnected' && <div className="publisher-connection-actions">{connection.state !== 'awaiting_callback' && <button className="button secondary" disabled={busy} onClick={() => connection.state === 'error' && onRecover ? onRecover(connection.id) : onTest(connection.id)}>{connection.state === 'error' && onRecover ? 'Repair & retry push' : connection.state === 'verified' ? 'Test push again' : 'Retry push test'}</button>}{onManage && <>{connection.state !== 'awaiting_callback' && <button className="button secondary" disabled={busy} onClick={() => onManage(connection, 'rotate')}>Rotate key</button>}<button className="button secondary publisher-danger" disabled={busy} onClick={() => onManage(connection, 'disconnect')}>Disconnect</button></>}</div>}
    {connection.state === 'disconnected' && connection.remoteDisablePending && onManage && <div className="publisher-connection-actions"><button className="button secondary" disabled={busy} onClick={() => onManage(connection, 'disconnect')}>Retry GitHub cleanup</button></div>}
  </article>
}

export function PublisherConfirmation({ connection, action, busy, error, onCancel, onConfirm }: { connection: Connection; action: ManagementAction; busy: boolean; error?: string; onCancel: () => void; onConfirm: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const cancel = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    const previous = document.activeElement
    dialog.current?.showModal(); cancel.current?.focus()
    return () => { if (previous instanceof HTMLElement && previous.isConnected) previous.focus() }
  }, [])
  const disconnect = action === 'disconnect'
  return <dialog className="publisher-confirmation" ref={dialog} aria-labelledby="publisher-confirmation-title" aria-describedby="publisher-confirmation-description" onCancel={event => { event.preventDefault(); if (!busy) onCancel() }}><div className="publisher-confirmation-body"><p className="eyebrow">PUBLISHER CONNECTION</p><h2 id="publisher-confirmation-title">{disconnect ? 'Disconnect' : 'Rotate key for'} {connection.repository.fullName}?</h2><p><code>{connection.imageRepository}</code></p><div id="publisher-confirmation-description">{disconnect ? <><p>Revoke this publisher’s local key and disable its owned notification configuration. Historical publications remain available.</p><p>Shared Tailscale access and federation are not deleted. Shared or changed GitHub resources are not removed.</p></> : <><p>Create a replacement key, update the repository secret, and dispatch a new push test. The old key stays active until Services receives the authenticated callback.</p><p>An accepted dispatch is not success. Keep the saved connection while verification completes.</p></>}</div>{error && <p className="notice danger" role="alert">{error}</p>}</div><footer className="publisher-footer"><button className="button secondary" ref={cancel} disabled={busy} onClick={onCancel}>Cancel</button><button className={`button primary ${disconnect ? 'publisher-danger' : ''}`} disabled={busy} onClick={onConfirm}>{busy ? 'Please wait…' : disconnect ? 'Disconnect publisher' : 'Rotate & test push'}</button></footer></dialog>
}

type FooterProps = { step: number; busy: boolean; available: boolean; canAdvance: boolean; workflowNeeded: boolean; pendingWorkflow: boolean; onBack: () => void; onAdvance: () => void; onCancel: () => void }
export function PublisherFooter({ step, busy, available, canAdvance, workflowNeeded, pendingWorkflow, onBack, onAdvance, onCancel }: FooterProps) {
  return <footer className="publisher-footer"><button className="button secondary" disabled={busy} onClick={onBack}>{step === 0 ? 'Cancel' : step === 3 ? 'Back to publishers' : 'Back'}</button><p>{busy ? 'Please wait…' : step === 0 ? 'Authorize both connections to continue.' : step === 2 ? pendingWorkflow ? 'Merge on GitHub, then request a fresh review. No merge polling.' : workflowNeeded ? 'Only the setup PR is created. Merge and review before applying.' : 'Only this explicit action changes GitHub repository configuration.' : step === 3 ? 'Saved state from Services, not GitHub publication polling.' : 'Review before making any changes.'}</p>{step !== 3 && <button className="button primary" disabled={busy || !available || !canAdvance} onClick={onAdvance}>{step === 0 ? 'Select repository →' : step === 1 ? 'Review setup →' : pendingWorkflow ? 'Review again →' : workflowNeeded ? 'Open setup PR ↗' : 'Apply & test push'}</button>}{step !== 0 && step !== 3 && <button className="publisher-cancel" disabled={busy} onClick={onCancel}>Cancel setup</button>}</footer>
}

type Props = { user: AuthUser; onBack: () => void; onAccounts: () => void; onLogout: () => void }
export function Publishers({ user, onBack, onAccounts, onLogout }: Props) {
  const [status, setStatus] = useState<PublisherStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const lock = useRef(false)
  const [error, setError] = useState('')
  const [step, setStep] = useState<number | null>(null)
  const [manifest, setManifest] = useState<GitHubStart | null>(null)
  const [registrationOwner, setRegistrationOwner] = useState<'personal' | 'organization'>('personal')
  const [organization, setOrganization] = useState('')
  const [tailnet, setTailnet] = useState({ clientId: '', audience: '', tags: '' })
  const [repositories, setRepositories] = useState<Repository[] | null>(null)
  const [query, setQuery] = useState('')
  const [repository, setRepository] = useState<Repository | null>(null)
  const [imageRepository, setImageRepository] = useState('')
  const [plan, setPlan] = useState<PublisherPlan | null>(null)
  const [prUrl, setPrUrl] = useState('')
  const [confirmation, setConfirmation] = useState<{ connection: Connection; action: ManagementAction } | null>(null)
  const [connectionId, setConnectionId] = useState('')
  const [revision, setRevision] = useState(0)
  const pendingCallback = awaitingCallback(status?.connections ?? [])
  useEffect(() => {
    let active = true
    const controller = new AbortController()
    const timeout = window.setTimeout(() => controller.abort(), 12_000)
    void api.status(controller.signal).then(next => { if (active) { setStatus(next); setTailnet(next.tailnet); setError('') } }).catch(cause => { if (active) setError(errorMessage(cause)) }).finally(() => { if (active) setLoading(false); window.clearTimeout(timeout) })
    return () => { active = false; controller.abort(); window.clearTimeout(timeout) }
  }, [revision])
  useEffect(() => {
    if (!pendingCallback || busy) return
    let active = true
    let timer: number
    let controller: AbortController | null = null
    const poll = async () => {
      controller = new AbortController()
      const timeout = window.setTimeout(() => controller?.abort(), 12_000)
      try { const next = await api.status(controller.signal); if (active) { setStatus(next); setError('') } }
      catch (cause) { if (active) setError(`Could not refresh push acknowledgement: ${errorMessage(cause)}`) }
      finally { window.clearTimeout(timeout); if (active) timer = window.setTimeout(() => void poll(), 3_000) }
    }
    timer = window.setTimeout(() => void poll(), 3_000)
    return () => { active = false; window.clearTimeout(timer); controller?.abort() }
  }, [pendingCallback, busy])
  useEffect(() => {
    const restore = () => { lock.current = false; setBusy(false) }
    window.addEventListener('pageshow', restore)
    return () => window.removeEventListener('pageshow', restore)
  }, [])
  const perform = async (action: () => Promise<void>) => {
    if (lock.current) return
    lock.current = true; setBusy(true); setError('')
    try { await action() } catch (cause) { await refreshAfterPublisherTimeout(cause, api, setStatus); setError(errorMessage(cause)) }
    finally { lock.current = false; setBusy(false) }
  }
  const reset = () => {
    if (lock.current) return
    setStep(null); setManifest(null); setRepositories(null); setQuery(''); setRepository(null); setImageRepository(''); setPlan(null); setPrUrl(''); setConnectionId(''); setError('')
  }
  const changeStep = (next: number) => { if (!lock.current) { setStep(next); setError(''); setManifest(null); setPrUrl(''); if (next < 2) setPlan(null) } }
  const refreshStatus = () => { if (!lock.current) { setError(''); setLoading(true); setRevision(value => value + 1) } }
  const loadRepositories = async () => {
    const result = await api.repositories()
    setRepositories(result.repositories)
    // Discovery can change installation IDs or remove access. Never retain a stale approval.
    setRepository(null); setImageRepository(''); setPlan(null)
  }
  const selectRepository = (next: Repository) => { setRepository(next); setImageRepository(`ghcr.io/${next.fullName.toLowerCase()}`); setPlan(null); setError('') }
  const startGitHub = () => void perform(async () => {
    const result = await api.startGitHub(registrationOwner === 'organization' ? organization : undefined)
    if (!githubManifestAction(result.action) || !result.manifest) throw new Error('The server returned an invalid GitHub consent destination. No handoff was made.')
    setManifest(result)
  })
  const saveTailnet = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    void perform(async () => { const next = await api.saveTailnet(tailnet); setStatus(await api.status()); setTailnet(next.tailnet); setPlan(null) })
  }
  const saveConnection = (connection: Connection) => {
    setStatus(current => current ? { ...current, connections: [...current.connections.filter(saved => saved.id !== connection.id), connection] } : current)
    setConnectionId(connection.id); setStep(3)
  }
  const testPush = (id: string) => void perform(async () => {
    const result = await api.test(id); saveConnection(result.connection); setStatus(await api.status())
  })
  const recoverPush = (id: string) => void perform(async () => {
    const result = await api.recover(id); saveConnection(result.connection); setStatus(await api.status())
  })
  const manageConnection = (connection: Connection, action: ManagementAction) => { if (!lock.current) { setError(''); setConfirmation({ connection, action }) } }
  const confirmManagement = () => void perform(async () => {
    if (!confirmation) return
    const result = await api[confirmation.action](confirmation.connection.id)
    setStatus(current => current ? { ...current, connections: current.connections.map(saved => saved.id === result.connection.id ? result.connection : saved) } : current)
    setConfirmation(null)
    setStatus(await api.status())
  })
  const advance = () => void perform(async () => {
    if (step === 0) { await loadRepositories(); setStep(1) }
    if (step === 1 && repository && !imageRepositoryProblem(imageRepository)) { const next = await api.plan(repository, imageRepository); setPlan(next); setStep(2) }
    if (step === 2 && prUrl && repository) {
      const next = await api.plan(repository, imageRepository); setPlan(next); setPrUrl('')
    } else if (step === 2 && canOpenWorkflowPlan(plan, repository, imageRepository) && repository && plan?.workflow) {
      const result = await api.workflow(repository, imageRepository, plan.workflow.fingerprint)
      setPrUrl(result.prUrl || 'unverified')
      if (!githubPullRequestUrl(result.prUrl, repository)) throw new Error('The server returned an untrusted setup PR link. No external link was opened. Review again before retrying.')
    } else if (step === 2 && canApplyPlan(plan, repository, imageRepository) && repository && plan) {
      const result = await applyAndRefresh(api, plan, next => {
        setStatus(next)
        const saved = next.connections.find(item => item.repository.id === repository.id && item.repository.installationId === repository.installationId && item.imageRepository === imageRepository && item.state !== 'disconnected')
        if (saved) saveConnection(saved)
      })
      saveConnection(result.connection)
    }
  })
  const registered = !!(status?.tailnet.clientId && status.tailnet.audience && status.tailnet.tags)
  const available = status?.available === true
  const readyForRepositories = available && status?.github.connected && registered
  const visible = repositories?.filter(item => item.fullName.toLowerCase().includes(query.toLowerCase())) ?? []
  const selectedConnection = status?.connections.find(item => item.id === connectionId)
  const workflowNeeded = canOpenWorkflowPlan(plan, repository, imageRepository)
  const canAdvance = step === 0 ? readyForRepositories : step === 1 ? !!repository && !imageRepositoryProblem(imageRepository) : step === 2 ? !!prUrl || workflowNeeded || canApplyPlan(plan, repository, imageRepository) : false

  return <><a className="skip-link" href="#publishers-content">Skip to publishers</a><header className="app-header publisher-header"><a className="brand" href="#monitor" onClick={event => { event.preventDefault(); if (!busy) onBack() }}>services</a><div className="auth-nav"><AppNavigation active="publishers" onMonitor={onBack} onAccounts={onAccounts} disabled={busy} /><span className="auth-username">{user.username}</span><button className="button secondary" onClick={onLogout} disabled={busy}>Sign out</button></div></header>
    <main className="workspace publishers-workspace" id="publishers-content"><section className="page-heading"><div><p className="eyebrow">INTEGRATIONS / GITHUB ACTIONS</p><h1>{step === null ? 'Image publishers' : 'Connect an image publisher'}</h1><p className="publisher-muted">Receive image updates directly from your publishing workflows.</p></div><button className="button secondary" disabled={busy || loading} onClick={refreshStatus}>Refresh</button></section>
      {error && <p className="notice danger" role="alert">{error}</p>}
      {loading && <p className="publisher-loading" role="status">Loading publisher configuration…</p>}
      {!status && !loading && <section className="publisher-panel publisher-empty"><h2>Publishers couldn’t be loaded</h2><p>Check your connection and use Refresh to try again.</p></section>}
      {status && <>{!available && <div className="publisher-info publisher-warning" role="alert"><strong>Publisher setup is unavailable</strong>{status.reason || 'This server is not configured for publisher setup.'}</div>}
        {step === null ? <><section className="publisher-panel"><div className="publisher-panel-head"><h2>Connected publishers</h2><span className="publisher-badge">{status.connections.length ? `${status.connections.length} configured` : 'No repositories connected'}</span></div><div className="publisher-intro"><div><p className="eyebrow">GITHUB → YOUR HOME SERVER</p><h2>Connect a repository.<br />Let the next publish announce itself.</h2><p>Services configures the existing publishing integration and verifies that GitHub Actions can reach this server. You approve the access and the exact configuration.</p><button className="button primary" disabled={busy || !available} onClick={() => changeStep(0)}>Connect GitHub repository →</button><div className="publisher-route"><span>GitHub Actions</span>→<span>Private Tailscale route</span>→<span>Services</span></div></div><aside className="publisher-summary"><h3>What this setup does</h3><ul><li>Creates a repository-scoped publisher key and transfers it directly to an Actions secret.</li><li>Reviews existing supported hooks or proposes a safe workflow diff in a setup PR when supported.</li><li>Tests real push connectivity without publishing an image.</li></ul><p>Publication notifications only.<br />Container updates still need your confirmation.</p></aside></div></section><div className="publisher-connections">{status.connections.map(connection => <PublisherConnection key={connection.id} connection={connection} busy={busy || !available} onTest={testPush} onRecover={recoverPush} onManage={manageConnection} />)}</div></>
          : <section className="publisher-panel publisher-wizard" aria-label="Publisher setup"><ol className="publisher-steps">{steps.map((name, index) => <li key={name} className={index === step ? 'active' : index < step ? 'done' : ''} aria-current={index === step ? 'step' : undefined}><span>{index < step ? '✓' : index + 1}</span>{name}</li>)}<li className="publisher-aside">GitHub Actions pushes metadata to Services over your private Tailscale connection.<br /><br />No automatic container updates.</li></ol><div className="publisher-wizard-content"><fieldset className="publisher-controls" disabled={busy || !available} aria-label="Publisher setup controls"><div className="publisher-step-content">
            {step === 0 && <><h2>Authorize your connections</h2><p>These connections can be reused when you add another publisher.</p><div className="publisher-auth-row"><div><h3>GitHub App</h3><p>{status.github.connected ? status.github.name || 'Services GitHub App is connected.' : 'GitHub displays its own external consent screen. Review permissions and approve only the repositories you choose.'}</p></div>{status.github.connected ? <span className="publisher-badge good">Connected</span> : <span className="publisher-badge">Not connected</span>}</div>
              {!status.github.connected && <GitHubRegistration owner={registrationOwner} organization={organization} onOwner={next => { setRegistrationOwner(next); setManifest(null); setError('') }} onOrganization={next => { setOrganization(next); setManifest(null); setError('') }} onSubmit={event => { event.preventDefault(); startGitHub() }} />}
              {manifest && <section className="publisher-info"><h3>Continue on GitHub</h3><p>You will leave Services to create and approve the GitHub App. GitHub returns you to Publishers. You can then install the app on selected repositories.</p><details><summary>Review nonsecret GitHub App manifest</summary><pre>{manifest.manifest}</pre></details><GitHubManifestForm manifest={manifest} lockRef={lock} /><button className="button secondary" onClick={() => setManifest(null)}>Cancel GitHub handoff</button></section>}
              {status.github.connected && <div className="publisher-info"><strong>Choose repository access on GitHub</strong><p>Install the app on the repositories you want to connect. After approving installation, return here and select a repository. Use Refresh repositories to discover newly granted access.</p>{status.github.installUrl && githubInstallUrl(status.github.installUrl) ? <a className="button secondary" href={status.github.installUrl} target="_blank" rel="noreferrer">Install on selected repositories ↗</a> : <p>Installation link unavailable. Refresh this page or check the GitHub App configuration.</p>}</div>}
              <div className="publisher-auth-row"><div><h3>Private delivery · Tailscale</h3><p>Register an existing federated CI identity once. Services does not automatically provision your tailnet and never requests an OAuth secret.</p></div><span className={`publisher-badge ${registered ? 'good' : ''}`}>{registered ? 'Registered' : 'Not registered'}</span></div>
              <form className="publisher-tailnet" onSubmit={saveTailnet}><label>Federated client ID<input value={tailnet.clientId} onChange={event => setTailnet({ ...tailnet, clientId: event.target.value })} required autoComplete="off" /></label><label>Audience<input value={tailnet.audience} onChange={event => setTailnet({ ...tailnet, audience: event.target.value })} required autoComplete="off" /></label><label>Runner tags<input value={tailnet.tags} onChange={event => setTailnet({ ...tailnet, tags: event.target.value })} required placeholder="tag:services-publisher" autoComplete="off" /></label><button className="button secondary" type="submit">Register existing federation</button></form>
              <div className="publisher-info">A tailnet administrator must configure federation trust for this repository/ref and allow the runner tags to reach this server over HTTPS. Registering these values does not grant network access.</div></>}
            {step === 1 && <><h2>Select a publishing repository</h2><p>Only repositories approved for the GitHub App appear here. Refresh after installing or changing access on GitHub.</p><div className="publisher-repository-tools"><label>Find repository<input type="search" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search repositories" /></label><button className="button secondary" onClick={() => void perform(loadRepositories)}>Refresh repositories</button></div>
              {repositories === null ? <p>No repositories loaded. Use Refresh repositories to retry.</p> : repositories.length === 0 ? <div className="publisher-info"><strong>No authorized repositories found</strong>Install the GitHub App on a repository, then refresh. GitHub installation discovery only runs when you ask for it.</div> : visible.length === 0 ? <p className="publisher-info">No matching repositories. Try another search.</p> : <div className="publisher-repository-list" role="radiogroup" aria-label="Publishing repository">{visible.map(item => <label className={`publisher-repository-choice ${item.id === repository?.id ? 'selected' : ''}`} key={`${item.installationId}:${item.id}`}><input type="radio" name="repository" value={item.id} checked={item.id === repository?.id && item.installationId === repository.installationId} onChange={() => selectRepository(item)} /><span><strong>{item.fullName}</strong><small>Default branch: {item.defaultBranch}</small></span></label>)}</div>}
              {repository && <><label className="publisher-image-field">Exact image repository<input value={imageRepository} onChange={event => { setImageRepository(event.target.value); setPlan(null); setError('') }} aria-describedby="publisher-image-help" aria-invalid={!!imageRepositoryProblem(imageRepository)} spellCheck={false} autoComplete="off" /></label><p id="publisher-image-help" className="publisher-muted">{imageRepositoryProblem(imageRepository) || 'Confirm the exact GHCR repository your workflow publishes. The suggested name is editable; it is not discovered from a publication.'}</p><dl className="publisher-facts"><div><dt>Repository</dt><dd>{repository.fullName}</dd></div><div><dt>Notification endpoint</dt><dd><code>{status.endpoint || 'Not configured'}</code></dd></div></dl><div className="publisher-info">This exact image repository becomes the publisher key’s allowlist. The key cannot read dashboard data or operate containers.</div></>}
            </>}
            {step === 2 && plan && (prUrl ? <PublisherWorkflowPending plan={plan} prUrl={prUrl} /> : <PublisherReview plan={plan} />)}
            {step === 2 && plan && plan.supported && !plan.workflow?.files.length && !canApplyPlan(plan, repository, imageRepository) && !plan.problems.length && <p className="notice danger" role="alert">The returned plan does not match this selection. Go Back and request a new review.</p>}
            {step === 3 && (selectedConnection ? <PublisherConnection connection={selectedConnection} busy={busy || !available} onTest={testPush} onRecover={recoverPush} onManage={manageConnection} /> : <p className="publisher-info">This connection was not present in the server’s latest response. Refresh to check its saved state.</p>)}
          </div></fieldset><PublisherFooter step={step} busy={busy} available={available} canAdvance={!!canAdvance} workflowNeeded={workflowNeeded} pendingWorkflow={!!prUrl} onBack={() => step === 0 || step === 3 ? reset() : changeStep(step - 1)} onAdvance={advance} onCancel={reset} /></div></section>}
        <p className="publisher-explain">Push delivery · no GitHub publication polling. Existing federation is registered, not automatically provisioned. Image installation always requires confirmation.</p></>}
    </main>{confirmation && <PublisherConfirmation connection={confirmation.connection} action={confirmation.action} busy={busy} error={error} onCancel={() => { if (!lock.current) { setConfirmation(null); setError('') } }} onConfirm={confirmManagement} />}</>
}
