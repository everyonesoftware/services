import { useEffect, useRef, useState } from 'react'
import type { FormEvent, RefObject } from 'react'
import { applyAndRefresh, awaitingCallback, canApplyPlan, canOpenWorkflowPlan, checkGitHubPermissionsAndRefresh, createPublisherApi, githubInstallUrl, githubManifestAction, githubPermissionsUrl, githubPullRequestUrl, githubRunUrl, githubVariablesReady, imageRepositoryProblem, orderPublisherConnections, readyForPublisherRepositories, refreshAfterPublisherTimeout } from './publisherApi.ts'
import type { Connection, GitHubStart, PublisherPlan, PublisherStatus, Repository, Tailnet, TailnetGuidance } from './publisherApi.ts'
import type { AuthUser } from './authApi.ts'
import './Publishers.css'
import { AppShell } from './AppShell.tsx'

export type { AppPage } from './AppShell.tsx'
export { AppNavigation } from './AppShell.tsx'

const api = createPublisherApi()
const errorMessage = (cause: unknown) => cause instanceof Error ? cause.message : 'Request failed. Please try again.'
const dateLabel = (value: string) => Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : 'Not available'
const steps = ['GitHub App', 'Repository', 'Tailscale', 'Review setup', 'Verify push']

function PublisherCopyValue({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState('')
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; clearTimeout(timer.current) }
  }, [])
  const copy = async () => {
    clearTimeout(timer.current)
    setCopied(false)
    setError('')
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(value)
      } else {
        const focused = document.activeElement
        const field = document.createElement('textarea')
        field.value = value
        field.style.cssText = 'position:fixed;left:-9999px;top:0'
        document.body.append(field)
        try {
          field.select()
          if (!document.execCommand('copy')) throw new Error('Clipboard unavailable')
        } finally {
          field.remove()
          if (focused instanceof HTMLElement) focused.focus()
        }
      }
      if (!mounted.current) return
      setCopied(true)
      timer.current = setTimeout(() => setCopied(false), 2000)
    } catch {
      if (mounted.current) setError('Could not copy. Select and copy the text manually.')
    }
  }
  return <><div className="publisher-copy-block"><code>{value}</code><button className="publisher-copy-value" type="button" aria-label={`${copied ? 'Copied' : 'Copy'} ${label}`} title={copied ? 'Copied' : `Copy ${label}`} data-copied={copied} onClick={() => void copy()}><svg viewBox="0 0 24 24" aria-hidden="true">{copied ? <path d="m5 12 4 4L19 6" /> : <><rect x="8" y="8" width="12" height="12" rx="2" /><path d="M16 8V4a1 1 0 0 0-1-1H4a1 1 0 0 0-1 1v11a1 1 0 0 0 1 1h4" /></>}</svg></button></div><span className="sr-only" role="status">{copied ? `${label} copied` : ''}</span>{error && <p className="publisher-copy-error" role="alert">{error}</p>}</>
}

export function PublisherTailscale({ repository, guidance, tailnet, endpoint, busy, onChange, onReload }: { repository: Repository; guidance: TailnetGuidance | null; tailnet: Tailnet; endpoint: string; busy: boolean; onChange: (value: Tailnet) => void; onReload: () => void }) {
  const runnerTag = tailnet.tags.trim() || 'tag:services-publisher'
  return <><h2>Set up Tailscale for {repository.fullName}</h2><p>Create a dedicated trust credential for this repository. Do not reuse a credential restricted to another repository.</p>
    <section className="publisher-info publisher-tailnet-guide publisher-identity-guide" aria-label="Create repository trust credential">
      <div className="publisher-identity-head"><div><h3>1. Create a federated identity</h3><p>Complete these steps in Tailscale as a tailnet administrator.</p></div><a className="button primary" href="https://login.tailscale.com/admin/settings/trust-credentials" target="_blank" rel="noreferrer">Open Tailscale ↗</a></div>
      <ol className="publisher-identity-steps">
        <li><p>Choose <b>Credential → OpenID Connect</b>. Paste this description.</p><PublisherCopyValue key={repository.fullName} label="description" value={repository.fullName.replace('/', '_')} /></li>
        <li><p>Select <b>GitHub Actions</b> as the issuer. For a custom issuer, paste this URL.</p>{guidance?.issuer ? <PublisherCopyValue key={guidance.issuer} label="issuer URL" value={guidance.issuer} /> : <p role="status">Load the repository’s current GitHub identity before creating the credential.</p>}</li>
        <li><p>Paste this exact <b>subject</b> for the <b>{repository.defaultBranch}</b> branch.</p>{guidance?.subject ? <PublisherCopyValue key={guidance.subject} label="subject" value={guidance.subject} /> : guidance ? <p role="alert">{guidance.subjectError || 'GitHub subject could not be determined. Review the repository OIDC settings before continuing.'}</p> : <p role="status">Load the repository’s current GitHub identity before creating the credential.</p>}<p className="publisher-muted">Restricted to this repository’s default branch. Do not use a wildcard.</p></li>
        <li><p>Under <b>Keys → Auth Keys → Write</b>, authorize your runner tag.</p><PublisherCopyValue key={runnerTag} label="runner tag" value={runnerTag} />{!tailnet.tags.trim() && <p className="publisher-muted">Example tag — use the runner tag authorized by your tailnet policy.</p>}</li>
        <li><p>Select <b>Generate credential</b>, then bring its <b>Client ID</b> and <b>Audience</b> back here.</p></li>
      </ol>
      <details className="publisher-identity-details"><summary>Advanced identity guidance &amp; troubleshooting</summary><p>These values cover default-branch jobs without a GitHub environment. Environment, release-tag, or custom subject contexts need a separately reviewed trust rule; do not broaden the subject to a wildcard. Services cannot create trust credentials or change your network policy.</p><p><a href="https://tailscale.com/docs/features/workload-identity-federation" target="_blank" rel="noreferrer">Workload identity federation instructions ↗</a> · <a href="https://tailscale.com/docs/integrations/github/github-action" target="_blank" rel="noreferrer">GitHub Action setup ↗</a></p></details>
      <button className="button secondary publisher-identity-refresh" type="button" disabled={busy} onClick={onReload}>Refresh GitHub identity</button>
    </section>
    <section className="publisher-info publisher-tailnet-guide"><h3>2. Allow private HTTPS delivery</h3><p>In your tailnet policy, allow only the authorized runner tag to reach this Services server over HTTPS. Credential scopes allow joining the tailnet; they do not grant network access.</p><p>Destination: <code>{endpoint}</code>. Use the HTTPS host and port from this URL in your rule, not the API path.</p><a href="https://login.tailscale.com/admin/acls" target="_blank" rel="noreferrer">Open Tailscale access controls ↗</a></section>
    <h3 className="publisher-tailnet-heading">3. Save this repository’s credential</h3><div className="publisher-tailnet"><label>Client ID<input value={tailnet.clientId} onChange={event => onChange({ ...tailnet, clientId: event.target.value })} required autoComplete="off" spellCheck={false} placeholder="Copy from the new trust credential" /></label><label>Audience<input value={tailnet.audience} onChange={event => onChange({ ...tailnet, audience: event.target.value })} required autoComplete="off" spellCheck={false} placeholder="Copy the exact Tailscale audience" /></label><label>Runner tags<input value={tailnet.tags} onChange={event => onChange({ ...tailnet, tags: event.target.value })} required placeholder="tag:services-publisher" autoComplete="off" spellCheck={false} /></label></div>
    <p className="publisher-muted">Use the audience exactly as shown by Tailscale. Do not add <code>https://</code>.</p>
    <p className="publisher-muted">Save &amp; review stores these nonsecret values only for {repository.fullName}. Access is not verified until the runner sends an authenticated connection proof.</p><details className="publisher-info"><summary>If the test fails with 403</summary><p>Open the credential linked in the workflow error and inspect its latest token exchange error. Check subject, audience, and custom claim rules first. A subject naming another repository cannot authorize this one.</p></details></>
}

export function GitHubManifestForm({ manifest, lockRef }: { manifest: GitHubStart; lockRef: RefObject<boolean> }) {
  return <form action={manifest.action} method="post" onSubmit={event => {
    if (lockRef.current) { event.preventDefault(); return }
    // Do not set busy here: disabling the enclosing fieldset before the browser
    // serializes this native POST omits the manifest, so GitHub receives no JSON.
    lockRef.current = true
  }}><input type="hidden" name="manifest" value={manifest.manifest} /><button className="button primary" type="submit">Continue to GitHub ↗</button></form>
}

export function GitHubPermissionNotice({ github, busy, onFinish }: { github: PublisherStatus['github']; busy: boolean; onFinish: () => void }) {
  if (!github.connected || githubVariablesReady(github)) return null
  return <section className="publisher-info publisher-warning" role="status"><strong>GitHub App permission setup is pending</strong><p>Finish the one-time Variables permission approval before installing or selecting repositories.</p><button className="button secondary" disabled={busy} onClick={onFinish}>Finish GitHub permission setup →</button></section>
}

export function GitHubRepositoryAccess({ github, busy }: { github: PublisherStatus['github']; busy: boolean }) {
  const installUrl = githubVariablesReady(github) && github.installUrl && githubInstallUrl(github.installUrl) ? github.installUrl : null
  return <section className="publisher-info" aria-label="GitHub App repository access"><strong>Install or manage repository access</strong>
    <p>Start from the GitHub App installation page—not from your repository list.</p>
    <ol><li>Open the installation page below and choose the account that owns your repositories.</li><li>Choose <b>Only select repositories</b> and select each repository that publishes an image.</li><li>Review access and click <b>Install</b>. If already installed, choose <b>Configure</b>, update repository access, and click <b>Save</b>.</li><li>Return to Services and click <b>Refresh repositories</b>, then select a repository to continue.</li></ol>
    {installUrl ? !busy && <a className="button secondary" href={installUrl} target="_blank" rel="noreferrer">Install on selected repositories ↗</a> : <p>Installation link unavailable. Go Back to GitHub App and look for Install on selected repositories after checking GitHub permissions.</p>}
    <p>For organization repositories, an organization owner may need to approve access. A private App must belong to that organization.</p>
  </section>
}

export function GitHubPermissions({ github, busy, onCheck }: { github: PublisherStatus['github']; busy: boolean; onCheck: () => void }) {
  if (!github.connected) return null
  const ready = githubVariablesReady(github)
  const checked = github.permissionsChecked === true && typeof github.variablesWrite === 'boolean'
  return <section className={`publisher-info ${ready ? '' : 'publisher-warning'}`} aria-label="GitHub Variables permission"><h3>One-time GitHub Variables permission</h3>
    <p>GitHub cannot request Variables permission during manifest registration. Finish this one-time approval manually; Services still configures repositories automatically after your review.</p>
    <p>GitHub App settings → Permissions &amp; events → Repository permissions → Variables → Read and write → Save.</p>
    {github.settingsUrl && githubPermissionsUrl(github.settingsUrl) ? !busy && <a className="button secondary" href={github.settingsUrl} target="_blank" rel="noreferrer">Open GitHub App permission settings ↗</a> : <p>Check permissions to load settings link.</p>}
    <p>For each existing installation, its account owner or organization owner must also approve updated access in GitHub installation settings → Configure. Saving App permissions alone does not update existing installation access.</p>
    <p role="status"><strong>{ready ? 'Variables write permission verified' : checked ? 'Variables write permission is missing' : 'Variables permission has not been checked'}</strong></p>
    <button className="button secondary" type="button" disabled={busy} onClick={onCheck}>Check GitHub permissions</button>
    <p className="publisher-muted">Permission checks run on demand, without GitHub polling. Before review and apply, the server makes a fresh check with the actual installation-scoped token.</p>
  </section>
}

type RegistrationProps = { owner: 'personal' | 'organization'; organization: string; onOwner: (owner: 'personal' | 'organization') => void; onOrganization: (value: string) => void; onSubmit: (event: FormEvent<HTMLFormElement>) => void }
export function GitHubRegistration({ owner, organization, onOwner, onOrganization, onSubmit }: RegistrationProps) {
  return <form className="publisher-registration" onSubmit={onSubmit}><label>Register GitHub App under<select value={owner} onChange={event => onOwner(event.target.value as RegistrationProps['owner'])}><option value="personal">Personal account</option><option value="organization">Organization</option></select></label>
    {owner === 'organization' && <><label>Organization name<input name="organization" required value={organization} onChange={event => onOrganization(event.target.value)} pattern="[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?" maxLength={39} placeholder="Your GitHub organization" autoComplete="off" spellCheck={false} /></label><p className="publisher-muted">Organization owner approval is required. To install on an organization’s repositories, a private App must be registered to that organization—not your personal account.</p></>}
    <button className="button primary" type="submit">Prepare GitHub consent ↗</button></form>
}

export function PublisherReview({ plan }: { plan: PublisherPlan }) {
  const workflowReady = canOpenWorkflowPlan(plan, plan.repository, plan.imageRepository)
  const problems = [...new Set([...plan.problems, ...(plan.workflow?.problems ?? [])])]
  return <><h2>Review publisher setup</h2><p>{plan.repository.fullName} · <code>{plan.imageRepository}</code></p>
    {workflowReady ? <div className="publisher-info publisher-warning"><strong>Workflow changes need a setup pull request</strong>Review the exact files below. Open setup PR only creates the proposed workflow change; no publisher key is created until you merge and review again.</div> : (!plan.supported || problems.length > 0 || !!plan.workflow?.files.length) && <div className="publisher-info publisher-warning" role="alert"><strong>{plan.workflow?.supported === false ? 'Services cannot automatically adapt this workflow' : 'Setup cannot be applied'}</strong><ul>{(problems.length ? problems : ['This repository does not have a supported existing notification workflow.']).map(problem => <li key={problem}>{problem}</li>)}</ul><p>{plan.workflow?.supported === false ? 'This does not mean your publishing workflow is broken. Services could not safely add its notification integration to this layout. Use the reasons above to adapt the integration manually, then return to the repository step and review again.' : 'Resolve the requirements above, then return to the repository step and review again.'}</p><a href="https://github.com/everyonesoftware/services#push-publisher-setup" target="_blank" rel="noreferrer">Publisher setup documentation ↗</a></div>}
    {workflowReady && <section className="publisher-workflow-files" aria-label="Proposed workflow diff">{plan.workflow!.files.map(file => <section className="publisher-workflow-file" key={file.path}><h3><code>{file.path}</code></h3><div className="publisher-diff"><section><h4>Before</h4><pre>{file.before || '(File does not exist)'}</pre></section><section><h4>After</h4><pre>{file.after || '(File removed)'}</pre></section></div></section>)}</section>}
    <div className="publisher-review"><section className="publisher-config" aria-label="Exact repository configuration"><h3>Actions variables · desired values</h3><dl>{Object.entries(plan.variables).sort(([left], [right]) => left.localeCompare(right)).map(([name, value]) => <div key={name}><dt><code>{name}</code></dt><dd><code>{value}</code></dd></div>)}</dl>{!Object.keys(plan.variables).length && <p>No variables proposed.</p>}</section>
      <section className="publisher-resources"><h3>Only on your approval</h3><ul><li>A dedicated <code>publications:write</code> key restricted to <code>{plan.imageRepository}</code>.</li><li>One encrypted Actions secret: <code>{plan.secretName}</code>. Its value is transferred server-to-server, never shown here.</li><li>Missing configuration is created. Existing values are not overwritten; conflicts stop setup.</li><li>A fresh, single-use push test. No image is built or published by this test.</li></ul></section></div>
    <div className="publisher-info"><strong>{workflowReady ? 'Separate approval, then verification' : 'Existing supported workflows only'}</strong>{workflowReady ? 'Workflow edits are proposed only through the setup PR. Merge it, then request a fresh review before applying configuration.' : 'No workflow files are changed by Apply. If automatic adaptation is unavailable, add the publication hook and test-dispatch workflow manually before applying configuration.'} Notification activation requires an authenticated callback; an accepted dispatch is not verification.</div>
  </>
}

export function PublisherWorkflowPending({ plan, prUrl }: { plan: PublisherPlan; prUrl: string }) {
  return <><h2>Waiting for workflow approval</h2><p>{plan.repository.fullName} · <code>{plan.imageRepository}</code></p><div className="publisher-info publisher-warning"><strong>Merge the setup PR</strong>Review and merge the proposed changes on GitHub. Services does not bypass repository rules or check merge status in the background. Then select Review again to inspect the current workflow before applying configuration.</div>{githubPullRequestUrl(prUrl, plan.repository) ? <a className="button secondary publisher-pr-link" href={prUrl} target="_blank" rel="noreferrer">Review setup pull request ↗</a> : <p className="notice danger" role="alert">The setup PR link could not be verified. No external link was opened.</p>}</>
}

type ManagementAction = 'disconnect' | 'rotate' | 'reconnect' | 'remove'
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
    {connection.state === 'disconnected' && onManage && <div className="publisher-connection-actions"><button className="button secondary" disabled={busy} onClick={() => onManage(connection, 'reconnect')}>Reconnect</button><button className="button secondary publisher-danger" disabled={busy} onClick={() => onManage(connection, 'remove')}>Remove</button>{connection.remoteDisablePending && <button className="button secondary" disabled={busy} onClick={() => onManage(connection, 'disconnect')}>Retry GitHub cleanup</button>}</div>}
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
  const destructive = disconnect || action === 'remove'
  const title = { disconnect: 'Disconnect', rotate: 'Rotate key for', reconnect: 'Reconnect', remove: 'Remove' }[action]
  const label = { disconnect: 'Disconnect publisher', rotate: 'Rotate & test push', reconnect: 'Reconnect & test push', remove: 'Remove publisher' }[action]
  const description = action === 'reconnect' ? <><p>Create a fresh credential, replace the GitHub Actions secret for this saved repository, and dispatch a new connectivity test. The revoked credential is never restored.</p><p>Notifications are enabled only after an authenticated callback verifies the new connection. Conflicting repository settings stop reconnection.</p></> : action === 'remove' ? <><p>Remove this disconnected connection from Services. Historical publications remain available.</p><p>GitHub resources and shared Tailscale access are not deleted. If GitHub cleanup is pending, retry cleanup before removing this record.</p></> : disconnect ? <><p>Revoke this publisher’s local key and disable its owned notification configuration. Historical publications remain available.</p><p>Shared Tailscale access and federation are not deleted. Shared or changed GitHub resources are not removed.</p></> : <><p>Create a replacement key, update the repository secret, and dispatch a new push test. The old key stays active until Services receives the authenticated callback.</p><p>An accepted dispatch is not success. Keep the saved connection while verification completes.</p></>
  return <dialog className="publisher-confirmation" ref={dialog} aria-labelledby="publisher-confirmation-title" aria-describedby="publisher-confirmation-description" onCancel={event => { event.preventDefault(); if (!busy) onCancel() }}><div className="publisher-confirmation-body"><p className="eyebrow">PUBLISHER CONNECTION</p><h2 id="publisher-confirmation-title">{title} {connection.repository.fullName}?</h2><p><code>{connection.imageRepository}</code></p><div id="publisher-confirmation-description">{description}</div>{error && <p className="notice danger" role="alert">{error}</p>}</div><footer className="publisher-footer"><button className="button secondary" ref={cancel} disabled={busy} onClick={onCancel}>Cancel</button><button className={`button primary ${destructive ? 'publisher-danger' : ''}`} disabled={busy} onClick={onConfirm}>{busy ? 'Please wait…' : label}</button></footer></dialog>
}

type FooterProps = { step: number; busy: boolean; available: boolean; canAdvance: boolean; workflowNeeded: boolean; pendingWorkflow: boolean; onBack: () => void; onAdvance: () => void; onCancel: () => void }
export function PublisherFooter({ step, busy, available, canAdvance, workflowNeeded, pendingWorkflow, onBack, onAdvance, onCancel }: FooterProps) {
  return <footer className="publisher-footer"><button className="button secondary" disabled={busy} onClick={onBack}>{step === 0 ? 'Cancel' : step === 4 ? 'Back to publishers' : 'Back'}</button><p>{busy ? 'Please wait…' : step === 0 ? 'Approve the GitHub App before selecting a repository.' : step === 2 ? 'Save federation settings for this repository only.' : step === 3 ? pendingWorkflow ? 'Merge on GitHub, then request a fresh review. No merge polling.' : workflowNeeded ? 'Only the setup PR is created. Merge and review before applying.' : 'Only this explicit action changes GitHub repository configuration.' : step === 4 ? 'Saved state from Services, not GitHub publication polling.' : 'Review before making any changes.'}</p>{step !== 4 && <button className="button primary" disabled={busy || !available || !canAdvance} onClick={onAdvance}>{step === 0 ? 'Select repository →' : step === 1 ? 'Set up Tailscale →' : step === 2 ? 'Save & review →' : pendingWorkflow ? 'Review again →' : workflowNeeded ? 'Open setup PR ↗' : 'Apply & test push'}</button>}{step !== 0 && step !== 4 && <button className="publisher-cancel" disabled={busy} onClick={onCancel}>Cancel setup</button>}</footer>
}

type Props = { user: AuthUser; onBack: () => void; onAccounts: () => void; onLogout: () => void; sessionError?: string }
export function Publishers({ user, onBack, onAccounts, onLogout, sessionError }: Props) {
  const [savedStatus, setStatus] = useState<PublisherStatus | null>(null)
  const status = savedStatus ? { ...savedStatus, connections: orderPublisherConnections(savedStatus.connections) } : null
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const lock = useRef(false)
  const [error, setError] = useState('')
  const [step, setStep] = useState<number | null>(null)
  const [manifest, setManifest] = useState<GitHubStart | null>(null)
  const [registrationOwner, setRegistrationOwner] = useState<'personal' | 'organization'>('personal')
  const [organization, setOrganization] = useState('')
  const [tailnet, setTailnet] = useState({ clientId: '', audience: '', tags: '' })
  const hydratedTailnetRepository = useRef<Repository | null>(null)
  const [guidance, setGuidance] = useState<TailnetGuidance | null>(null)
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
    void api.status(controller.signal).then(next => { if (active) { setStatus(next); setError('') } }).catch(cause => { if (active) setError(errorMessage(cause)) }).finally(() => { if (active) setLoading(false); window.clearTimeout(timeout) })
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
    hydratedTailnetRepository.current = null
    setStep(null); setManifest(null); setRepositories(null); setQuery(''); setRepository(null); setImageRepository(''); setPlan(null); setPrUrl(''); setConnectionId(''); setError(''); setGuidance(null); setTailnet({ clientId: '', audience: '', tags: '' })
  }
  const changeStep = (next: number) => { if (!lock.current) { setStep(next); setError(''); setManifest(null); setPrUrl(''); if (next < 3) setPlan(null) } }
  const refreshStatus = () => { if (!lock.current) { setError(''); setLoading(true); setRevision(value => value + 1) } }
  const checkGitHubPermissions = () => void perform(async () => {
    if (!status) return
    await checkGitHubPermissionsAndRefresh(api, status, setStatus, () => {
      setRepositories(null); setQuery(''); setRepository(null); setImageRepository(''); setPlan(null); setPrUrl(''); setGuidance(null)
    })
  })
  const loadRepositories = async () => {
    if (!readyForPublisherRepositories(status)) return
    const result = await api.repositories()
    setRepositories(result.repositories)
    // Discovery can change installation IDs or remove access. Never retain a stale approval.
    setRepository(null); setImageRepository(''); setPlan(null); setGuidance(null); setTailnet({ clientId: '', audience: '', tags: '' })
  }
  const selectRepository = (next: Repository) => { hydratedTailnetRepository.current = null; setRepository(next); setImageRepository(`ghcr.io/${next.fullName.toLowerCase()}`); setPlan(null); setGuidance(null); setTailnet({ clientId: '', audience: '', tags: '' }); setError('') }
  const startGitHub = () => void perform(async () => {
    const result = await api.startGitHub(registrationOwner === 'organization' ? organization : undefined)
    if (!githubManifestAction(result.action) || !result.manifest) throw new Error('The server returned an invalid GitHub consent destination. No handoff was made.')
    setManifest(result)
  })
  const loadGuidance = async () => {
    if (!repository) return
    setGuidance(null)
    const next = await api.tailnetGuidance(repository)
    setGuidance(next)
    // Identity refresh and Back/forward must not discard this repository's draft.
    // Selecting a different repository uses a different object and hydrates anew.
    if (hydratedTailnetRepository.current !== repository) {
      setTailnet(next.tailnet); hydratedTailnetRepository.current = repository
    }
  }
  const saveConnection = (connection: Connection) => {
    setStatus(current => current ? { ...current, connections: [...current.connections.filter(saved => saved.id !== connection.id), connection] } : current)
    setConnectionId(connection.id); setStep(4)
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
    try {
      if (confirmation.action === 'remove') {
        const result = await api.remove(confirmation.connection.id)
        if (!result.removed) throw new Error('Removal was not confirmed. Refresh saved state before retrying.')
        setStatus(current => current ? { ...current, connections: current.connections.filter(saved => saved.id !== confirmation.connection.id) } : current)
      } else {
        const result = await api[confirmation.action](confirmation.connection.id)
        setStatus(current => current ? { ...current, connections: current.connections.map(saved => saved.id === result.connection.id ? result.connection : saved) } : current)
      }
    } catch (cause) {
      // A failed remote operation can still persist local progress. Keep the
      // confirmation/error visible, but never leave its card showing stale state.
      try { setStatus(await api.status()) } catch { /* Preserve the original error. */ }
      throw cause
    }
    setConfirmation(null)
    setStatus(await api.status())
  })
  const advance = () => void perform(async () => {
    if (step === 0) { if (!readyForPublisherRepositories(status)) return; await loadRepositories(); setStep(1) }
    if (step === 1 && repository && !imageRepositoryProblem(imageRepository)) { setStep(2); await loadGuidance() }
    if (step === 2 && repository && guidance?.subject) { setStatus(await api.saveTailnet({ ...tailnet, repository })); setPlan(null); const next = await api.plan(repository, imageRepository); setPlan(next); setStep(3) }
    if (step === 3 && prUrl && repository) {
      const next = await api.plan(repository, imageRepository); setPlan(next); setPrUrl('')
    } else if (step === 3 && canOpenWorkflowPlan(plan, repository, imageRepository) && repository && plan?.workflow) {
      const result = await api.workflow(repository, imageRepository, plan.workflow.fingerprint)
      setPrUrl(result.prUrl || 'unverified')
      if (!githubPullRequestUrl(result.prUrl, repository)) throw new Error('The server returned an untrusted setup PR link. No external link was opened. Review again before retrying.')
    } else if (step === 3 && canApplyPlan(plan, repository, imageRepository) && repository && plan) {
      const result = await applyAndRefresh(api, plan, next => {
        setStatus(next)
        const saved = next.connections.find(item => item.repository.id === repository.id && item.repository.installationId === repository.installationId && item.imageRepository === imageRepository && item.state !== 'disconnected')
        if (saved) saveConnection(saved)
      })
      saveConnection(result.connection)
    }
  })

  const available = status?.available === true
  const readyForRepositories = readyForPublisherRepositories(status)
  const visible = repositories?.filter(item => item.fullName.toLowerCase().includes(query.toLowerCase())) ?? []
  const selectedConnection = status?.connections.find(item => item.id === connectionId)
  const workflowNeeded = canOpenWorkflowPlan(plan, repository, imageRepository)
  const canAdvance = step === 0 ? readyForRepositories : step === 1 ? !!repository && !imageRepositoryProblem(imageRepository) : step === 2 ? !!guidance?.subject && [tailnet.clientId, tailnet.audience, tailnet.tags].every(value => !!value && !/\s/.test(value)) : step === 3 ? !!prUrl || workflowNeeded || canApplyPlan(plan, repository, imageRepository) : false

  return <AppShell active="publishers" user={user} onMonitor={onBack} onAccounts={onAccounts} onLogout={onLogout} sessionError={sessionError} disabled={busy}>
    <main className={`workspace publishers-workspace${step === null ? '' : ' publisher-setup-workspace'}`} id="publishers-content"><section className="page-heading"><div><h1>{step === null ? 'Publishers' : 'Connect an image publisher'}</h1><p className="publisher-muted">Receive image updates directly from your publishing workflows.</p></div><button className="button secondary" disabled={busy || loading} onClick={refreshStatus}>Refresh</button></section>
      {error && <p className="notice danger" role="alert">{error}</p>}
      {loading && <p className="publisher-loading" role="status">Loading publisher configuration…</p>}
      {!status && !loading && <section className="publisher-panel publisher-empty"><h2>Publishers couldn’t be loaded</h2><p>Check your connection and use Refresh to try again.</p></section>}
      {status && <>{!available && <div className="publisher-info publisher-warning" role="alert"><strong>Publisher setup is unavailable</strong>{status.reason || 'This server is not configured for publisher setup.'}</div>}
        {step === null && <GitHubPermissionNotice github={status.github} busy={busy} onFinish={() => changeStep(0)} />}
        {step === null ? <><section className="publisher-list-heading" aria-label="Publisher connections"><div><h2>Repository connections <span className="count">{status.connections.length}</span></h2><p className="publisher-muted">{status.github.connected ? 'GitHub App connected. Manage repository access and push delivery.' : 'Connect your GitHub App to configure a repository publisher.'}</p></div><button className="button primary" disabled={busy || !available} onClick={() => changeStep(0)}>Connect GitHub repository →</button></section>{status.connections.length === 0 && <section className="publisher-panel publisher-empty"><h2>No publishers connected</h2><p>Connect a repository to review its configuration and verify push delivery. Image installation still requires your confirmation.</p></section>}<div className="publisher-connections">{status.connections.map(connection => <PublisherConnection key={connection.id} connection={connection} busy={busy || !available} onTest={testPush} onRecover={recoverPush} onManage={manageConnection} />)}</div></>
          : <section className="publisher-panel publisher-wizard" aria-label="Publisher setup"><ol className="publisher-steps">{steps.map((name, index) => <li key={name} className={index === step ? 'active' : index < step ? 'done' : ''} aria-current={index === step ? 'step' : undefined}><span>{index < step ? '✓' : index + 1}</span>{name}</li>)}<li className="publisher-aside">GitHub Actions pushes metadata to Services over your private Tailscale connection.<br /><br />No automatic container updates.</li></ol><div className="publisher-wizard-content"><fieldset className="publisher-controls" disabled={busy || !available} aria-label="Publisher setup controls"><div className="publisher-step-content" key={step} role="region" aria-label="Publisher step content" tabIndex={0}>
            {step === 0 && <><h2>Connect your GitHub App</h2><p>The GitHub App can be reused for repositories you explicitly authorize. Tailscale trust is configured separately after selecting a repository.</p><div className="publisher-auth-row"><div><h3>GitHub App</h3><p>{status.github.connected ? status.github.name || 'Services GitHub App is connected.' : 'GitHub displays its own external consent screen. Review permissions and approve only the repositories you choose.'}</p></div>{status.github.connected ? <span className="publisher-badge good">Connected</span> : <span className="publisher-badge">Not connected</span>}</div>
              {!status.github.connected && <GitHubRegistration owner={registrationOwner} organization={organization} onOwner={next => { setRegistrationOwner(next); setManifest(null); setError('') }} onOrganization={next => { setOrganization(next); setManifest(null); setError('') }} onSubmit={event => { event.preventDefault(); startGitHub() }} />}
              {manifest && <section className="publisher-info"><h3>Continue on GitHub</h3><p>You will leave Services to create and approve the GitHub App. GitHub returns you to Publishers. You can then install the app on selected repositories.</p><details><summary>Review nonsecret GitHub App manifest</summary><pre>{manifest.manifest}</pre></details><GitHubManifestForm manifest={manifest} lockRef={lock} /><button className="button secondary" onClick={() => setManifest(null)}>Cancel GitHub handoff</button></section>}
              <GitHubPermissions github={status.github} busy={busy} onCheck={checkGitHubPermissions} />
              </>}
            {step === 1 && <><h2>Select a publishing repository</h2><p>Only repositories approved for the GitHub App appear here.</p><GitHubRepositoryAccess github={status.github} busy={busy} /><div className="publisher-repository-tools"><label>Find repository<input type="search" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search repositories" /></label><button className="button secondary" onClick={() => void perform(loadRepositories)}>Refresh repositories</button></div>
              {repositories === null ? <p>No repositories loaded. Use Refresh repositories to retry.</p> : repositories.length === 0 ? <div className="publisher-info"><strong>No authorized repositories found</strong>Install the GitHub App on a repository, then refresh. GitHub installation discovery only runs when you ask for it.</div> : visible.length === 0 ? <p className="publisher-info">No matching repositories. Try another search.</p> : <div className="publisher-repository-list" role="radiogroup" aria-label="Publishing repository">{visible.map(item => <label className={`publisher-repository-choice ${item.id === repository?.id ? 'selected' : ''}`} key={`${item.installationId}:${item.id}`}><input type="radio" name="repository" value={item.id} checked={item.id === repository?.id && item.installationId === repository.installationId} onChange={() => selectRepository(item)} /><span><strong>{item.fullName}</strong><small>Default branch: {item.defaultBranch}</small></span></label>)}</div>}
              {repository && <><label className="publisher-image-field">Exact image repository<input value={imageRepository} onChange={event => { setImageRepository(event.target.value); setPlan(null); setError('') }} aria-describedby="publisher-image-help" aria-invalid={!!imageRepositoryProblem(imageRepository)} spellCheck={false} autoComplete="off" /></label><p id="publisher-image-help" className="publisher-muted">{imageRepositoryProblem(imageRepository) || 'Confirm the exact GHCR repository your workflow publishes. The suggested name is editable; it is not discovered from a publication.'}</p><dl className="publisher-facts"><div><dt>Repository</dt><dd>{repository.fullName}</dd></div><div><dt>Notification endpoint</dt><dd><code>{status.endpoint || 'Not configured'}</code></dd></div></dl><div className="publisher-info">This exact image repository becomes the publisher key’s allowlist. The key cannot read dashboard data or operate containers.</div></>}
            </>}
            {step === 2 && repository && <PublisherTailscale repository={repository} guidance={guidance} tailnet={tailnet} endpoint={status.endpoint} busy={busy} onChange={next => { setTailnet(next); setPlan(null) }} onReload={() => void perform(loadGuidance)} />}
            {step === 3 && plan && (prUrl ? <PublisherWorkflowPending plan={plan} prUrl={prUrl} /> : <PublisherReview plan={plan} />)}
            {step === 3 && plan && plan.supported && !plan.workflow?.files.length && !canApplyPlan(plan, repository, imageRepository) && !plan.problems.length && <p className="notice danger" role="alert">The returned plan does not match this selection. Go Back and request a new review.</p>}
            {step === 4 && (selectedConnection ? <PublisherConnection connection={selectedConnection} busy={busy || !available} onTest={testPush} onRecover={recoverPush} onManage={manageConnection} /> : <p className="publisher-info">This connection was not present in the server’s latest response. Refresh to check its saved state.</p>)}
          </div></fieldset><PublisherFooter step={step} busy={busy} available={available} canAdvance={!!canAdvance} workflowNeeded={workflowNeeded} pendingWorkflow={!!prUrl} onBack={() => step === 0 || step === 4 ? reset() : changeStep(step - 1)} onAdvance={advance} onCancel={reset} /></div></section>}
        </>}
    </main>{confirmation && <PublisherConfirmation connection={confirmation.connection} action={confirmation.action} busy={busy} error={error} onCancel={() => { if (!lock.current) { setConfirmation(null); setError('') } }} onConfirm={confirmManagement} />}</AppShell>
}
