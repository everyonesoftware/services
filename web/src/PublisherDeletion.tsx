import { useEffect, useRef, useState } from 'react'
import { githubPullRequestUrl } from './publisherApi.ts'
import type { Connection, createPublisherApi, DeletionPlan, PublisherStatus } from './publisherApi.ts'

type Props = {
  connection: Connection
  api: ReturnType<typeof createPublisherApi>
  onStatus: (status: PublisherStatus) => void
  onBusy: (busy: boolean) => void
  onClose: () => void
}
const message = (cause: unknown) => cause instanceof Error ? cause.message : 'Request failed. Please try again.'
const tailscaleCredentialsUrl = 'https://console.tailscale.com/admin/settings/trust-credentials'

export function PublisherDeletion({ connection, api, onStatus, onBusy, onClose }: Props) {
  const dialog = useRef<HTMLDialogElement>(null)
  const close = useRef<HTMLButtonElement>(null)
  const lock = useRef(false)
  const [plan, setPlan] = useState<DeletionPlan | null>(null)
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const [scope, setScope] = useState<'' | 'dedicated' | 'repository'>('')
  const [clientId, setClientId] = useState('')
  const [attested, setAttested] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  const phase = !plan ? 1 : plan.workflowVerified ? plan.tailscaleConfirmed ? 4 : 3 : plan.prUrl ? 2 : 1
  const workflowFiles = plan?.workflow.files ?? []
  const workflowProblems = plan?.workflow.problems ?? []
  const canAttest = !!(plan?.workflowVerified && plan.tailscaleReview && plan.tailscale.clientId && clientId === plan.tailscale.clientId && attested && scope && (!plan.tailscale.shared || scope === 'repository'))
  const perform = async (action: () => Promise<DeletionPlan | null>) => {
    if (lock.current) return
    lock.current = true; setBusy(true); onBusy(true); setError('')
    try {
      const next = await action()
      const saved = await api.status()
      if (next === null && saved.connections.some(item => item.id === connection.id)) throw new Error('Deletion was not confirmed by saved status. The publisher is retained; refresh before retrying.')
      onStatus(saved)
      if (next === null) onClose()
      else setPlan(next)
    } catch (cause) {
      // A rejected request may have saved progress. Never infer rollback, or
      // retain old approval if the fresh plan can no longer be read.
      setPlan(null)
      setAttested(false)
      setConfirmed(false)
      let refreshError = ''
      try { onStatus(await api.status()) } catch (failure) { refreshError = ` Saved status could not be refreshed: ${message(failure)}` }
      try {
        const next = await api.deletionPlan(connection.id)
        onStatus(await api.status())
        setPlan(next)
      } catch (failure) { refreshError += ` Deletion plan could not be refreshed: ${message(failure)}` }
      setError(message(cause) + refreshError)
    } finally { lock.current = false; setBusy(false); onBusy(false) }
  }
  const saveAttestation = () => {
    if (!canAttest || !plan || !scope) return
    void perform(async () => {
      const current = await api.deletionPlan(connection.id)
      if (!current.workflowVerified || current.tailscaleReview !== plan.tailscaleReview || (['clientId', 'audience', 'tags', 'shared'] as const).some(field => current.tailscale[field] !== plan.tailscale[field])) throw new Error('Tailscale settings or workflow changed. Review the refreshed plan and confirm the exact saved access again.')
      return await api.deletionTailscale(connection.id, { confirmed: true, clientId, scope, reviewFingerprint: plan.tailscaleReview })
    })
  }
  const finish = () => {
    if (!plan?.workflowVerified || !plan.tailscaleConfirmed || !confirmed) return
    void perform(async () => {
      const result = await api.disconnect(connection.id)
      onStatus(await api.status())
      if (result.connection.id !== connection.id || result.connection.state !== 'disconnected' || result.connection.remoteDisablePending === true) throw new Error('GitHub disable is not confirmed. The publisher is retained; retry cleanup before deleting.')
      const removed = await api.remove(connection.id)
      if (removed.removed !== true) throw new Error('Deletion was not confirmed. The publisher is retained; refresh before retrying.')
      return null
    })
  }
  useEffect(() => {
    const previous = document.activeElement
    let active = true
    dialog.current?.showModal()
    onBusy(true)
    void api.deletionPlan(connection.id).then(async next => {
      const status = await api.status()
      if (active) { onStatus(status); setPlan(next) }
    }).catch(async cause => {
      try { const status = await api.status(); if (active) onStatus(status) } catch { /* Preserve the plan failure. */ }
      if (active) setError(message(cause))
    }).finally(() => {
      if (active) { setBusy(false); onBusy(false); close.current?.focus() }
    })
    return () => { active = false; if (previous instanceof HTMLElement && previous.isConnected) previous.focus() }
  }, [api, connection.id, onStatus, onBusy])
  return <dialog className="publisher-confirmation publisher-deletion" ref={dialog} aria-labelledby="publisher-deletion-title" onCancel={event => { event.preventDefault(); if (!busy) onClose() }}>
    <div className="publisher-confirmation-body"><p className="eyebrow">DELETE PUBLISHER</p><h2 id="publisher-deletion-title">{['Remove workflow integration', 'Merge & verify cleanup', 'Revoke Tailscale credentials', 'Delete publisher?'][phase - 1]}</h2><p>{connection.repository.fullName} · <code>{connection.imageRepository}</code></p>
      <ol className="publisher-deletion-steps" aria-label="Deletion progress">{['Workflow cleanup', 'Merge & verify', 'Tailscale credentials', 'Delete publisher'].map((label, index) => <li key={label} aria-current={index + 1 === phase ? 'step' : undefined}>{index + 1} · {label}</li>)}</ol>
      {busy && <p role="status">Please wait — refreshing saved deletion state…</p>}
      {plan && phase === 1 && <><p>Review the actual workflow removal diff. Image builds, registry publishing, and unrelated jobs must remain unchanged.</p><section className="publisher-workflow-files" aria-label="Proposed workflow removal diff">{workflowFiles.map(file => <section className="publisher-workflow-file" key={file.path}><h3><code>{file.path}</code></h3><div className="publisher-diff"><section><h4>Before</h4><pre>{file.before || '(File does not exist)'}</pre></section><section><h4>After</h4><pre>{file.after || '(File removed)'}</pre></section></div></section>)}</section><p className="publisher-info">Cleanup is proposed through a pull request, never a direct default-branch write. Services will not merge it or bypass repository rules.</p>{(!plan.workflow.supported || workflowProblems.length > 0 || !workflowFiles.length || !plan.workflow.fingerprint) && <div className="publisher-info publisher-warning" role="alert"><strong>Automatic workflow cleanup is blocked</strong><ul>{(workflowProblems.length ? workflowProblems : ['No safe, fingerprinted cleanup is available. Refresh the plan after manual review.']).map(problem => <li key={problem}>{problem}</li>)}</ul><p>Do not remove shared steps or access. Resolve the workflow manually, then refresh the plan.</p></div>}</>}
      {plan && phase === 2 && <><p>Review and merge the cleanup pull request on GitHub, then verify the current default branch. Services does not poll for a merge.</p>{githubPullRequestUrl(plan.prUrl, connection.repository) ? <a className="button secondary publisher-pr-link" href={plan.prUrl} target="_blank" rel="noopener noreferrer">Review cleanup pull request ↗</a> : <p className="notice danger" role="alert">The cleanup PR link could not be verified for this repository. No external link was opened.</p>}<p className="publisher-info">{connection.state === 'disconnected' ? 'Publisher remains disconnected while cleanup is pending.' : 'Publisher remains connected until the final disconnect & delete confirmation.'}</p></>}
      {plan && phase === 3 && <><p>The server verified workflow removal on the current default branch. Now remove this repository’s Tailscale access manually.</p>
        <p className="publisher-info"><strong>This is a manual attestation, not automatic Tailscale verification.</strong>Services holds no Tailscale administrator credentials and cannot revoke or independently verify trust-credential removal.</p>
        <p><a className="button secondary publisher-pr-link" href={tailscaleCredentialsUrl} target="_blank" rel="noopener noreferrer">Open Tailscale credentials ↗</a></p>
        <dl className="publisher-facts"><div><dt>Saved Client ID</dt><dd><code>{plan.tailscale.clientId || 'Not configured — establish a safe scope before continuing.'}</code></dd></div><div><dt>Saved audience</dt><dd><code>{plan.tailscale.audience || 'Not configured'}</code></dd></div><div><dt>Saved runner tags</dt><dd><code>{plan.tailscale.tags || 'Not configured'}</code></dd></div><div><dt>Credential ownership</dt><dd>{plan.tailscale.shared ? 'Shared with other publishers' : 'Confirm dedicated ownership or repository-only scope'}</dd></div></dl>
        <div className="publisher-info publisher-warning">{plan.tailscale.shared ? <p><strong>Do not revoke the shared credential.</strong>Remove only this repository’s trust binding and retain access used by other publishers.</p> : <p>Revoke a dedicated credential only after confirming it is exclusive to this publisher. Otherwise remove only this repository’s trust binding.</p>}<p>Local reuse detection cannot rule out external users. Confirm ownership in the correct tailnet; absence of detected sharing is not proof of exclusive use.</p><p>If ownership is unknown or the repository cannot be isolated safely, stop here for an administrator’s review. Removing GitHub variables alone is not revocation.</p><p>Review issued auth keys and runner devices separately. Revoking a trust credential does not revoke its issued auth keys.</p></div>
        <fieldset className="publisher-controls publisher-deletion-controls" disabled={busy}><label htmlFor="publisher-deletion-scope">Credential scope</label><select id="publisher-deletion-scope" value={scope} onChange={event => { setScope(event.target.value as typeof scope); setAttested(false) }}><option value="">Choose a safely established scope</option><option value="dedicated" disabled={plan.tailscale.shared}>Dedicated credential — revoked only this publisher’s credential</option><option value="repository">Repository binding — removed only this repository’s access</option></select><label>Confirm saved Client ID<input value={clientId} onChange={event => { setClientId(event.target.value); setAttested(false) }} autoComplete="off" spellCheck={false} /></label><label className="publisher-deletion-check"><input type="checkbox" checked={attested} onChange={event => setAttested(event.target.checked)} /><span>I manually removed this publisher’s Tailscale access in the scope selected above.</span></label></fieldset>
      </>}
      {plan && phase === 4 && <><p className="publisher-info"><strong>Workflow removal verified. Manual Tailscale confirmation saved.</strong>Tailscale access removal was attested by an administrator, not independently verified by Services.</p><p className="publisher-info publisher-warning">Disconnect &amp; delete revokes this publisher’s local key and disables only its owned GitHub notification configuration, then removes the saved publisher. Historical publications remain available. Shared credentials and successor publisher configuration must not be removed. If GitHub cleanup fails, the publisher record is retained for retry.</p><label className="publisher-deletion-check"><input type="checkbox" disabled={busy} checked={confirmed} onChange={event => setConfirmed(event.target.checked)} /><span>I confirm disconnecting and deleting this publisher from Services.</span></label></>}
      {error && <p className="notice danger" role="alert">{error}</p>}
    </div><footer className="publisher-footer">
      <button className="button secondary" ref={close} disabled={busy} onClick={onClose}>Close — finish later</button>
      {(!plan || phase === 1) && <button className="button secondary" disabled={busy} onClick={() => void perform(() => api.deletionPlan(connection.id))}>Refresh plan</button>}
      {plan && phase === 1 && <button className="button primary" disabled={busy || !plan.workflow.supported || !!workflowProblems.length || !plan.workflow.fingerprint || !workflowFiles.length} onClick={() => void perform(() => api.deletionWorkflow(connection.id, plan.workflow.fingerprint))}>Open cleanup PR</button>}
      {plan && phase === 2 && <button className="button primary" disabled={busy} onClick={() => void perform(() => api.deletionVerify(connection.id))}>Verify workflow cleanup</button>}
      {plan && phase === 3 && <button className="button primary publisher-danger" disabled={busy || !canAttest} onClick={saveAttestation}>Save manual confirmation</button>}
      {plan && phase === 4 && <button className="button primary publisher-danger" disabled={busy || !confirmed} onClick={finish}>Disconnect &amp; delete</button>}
    </footer>
  </dialog>
}
