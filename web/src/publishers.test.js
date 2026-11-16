import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'vite'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())
const api = await server.ssrLoadModule('/src/publisherApi.ts').catch(() => ({}))
const ui = await server.ssrLoadModule('/src/Publishers.tsx').catch(() => ({}))
const repository = { id: 14, fullName: 'Acme/Website', defaultBranch: 'main', installationId: 8 }

test('deletion requests bind exact connection, reviewed fingerprint and manual credential attestation', async () => {
  const calls = []
  const client = api.createPublisherApi(async (url, options) => { calls.push([url, options.method, JSON.parse(options.body)]); return Response.json({ workflowVerified: false }) })
  assert.equal(typeof client.deletionPlan, 'function')
  await client.deletionPlan('saved/key')
  await client.deletionWorkflow('saved/key', 'exact-fingerprint')
  await client.deletionVerify('saved/key')
  await client.deletionTailscale('saved/key', { confirmed: true, clientId: 'saved-client', scope: 'repository', reviewFingerprint: 'exact-tailnet-review' })
  assert.deepEqual(calls, [
    ['/api/publishers/saved%2Fkey/deletion/plan', 'POST', {}],
    ['/api/publishers/saved%2Fkey/deletion/workflow', 'POST', { fingerprint: 'exact-fingerprint' }],
    ['/api/publishers/saved%2Fkey/deletion/verify', 'POST', {}],
    ['/api/publishers/saved%2Fkey/deletion/tailscale', 'POST', { confirmed: true, clientId: 'saved-client', scope: 'repository', reviewFingerprint: 'exact-tailnet-review' }],
  ])
})

test('Tailscale setup explains a repository-specific trust with exact fetched subject and safe links', () => {
  assert.equal(typeof ui.PublisherTailscale, 'function')
  const guidance = { issuer: 'https://token.actions.githubusercontent.com', subject: 'repo:Acme@42/Website@14:ref:refs/heads/main', tailnet: { clientId: '', audience: '', tags: '' } }
  const props = { repository, guidance, tailnet: { clientId: 'finance-client', audience: 'api.tailscale.com/finance-client', tags: 'tag:services-publisher' }, endpoint: 'https://services.test/api/images/published', busy: false, onChange() {}, onReload() {} }
  const html = renderToStaticMarkup(createElement(ui.PublisherTailscale, props))
  for (const text of ['Acme/Website', guidance.issuer, guidance.subject, 'Auth Keys', 'Write', 'tag:services-publisher', 'HTTPS', 'Client ID', 'Audience', '403', 'not verified', 'another repository']) assert.ok(html.includes(text), text)
  assert.match(html, /href="https:\/\/login.tailscale.com\/admin\/settings\/trust-credentials"/)
  assert.match(html, /href="https:\/\/tailscale.com\/docs\/features\/workload-identity-federation"/)
  assert.doesNotMatch(html, /OAuth secret|Register existing federation/)
  for (const [fullName, description] of [['everyonesoftware/finance-typescript', 'everyonesoftware_finance-typescript'], ['Acme-Team/Web_site.v2', 'Acme-Team_Web_site.v2']]) {
    const instructions = renderToStaticMarkup(createElement(ui.PublisherTailscale, { ...props, repository: { ...repository, fullName } }))
    assert.match(instructions, /Paste this description/)
    assert.ok(instructions.includes(`<code>${description}</code>`))
    assert.match(instructions, /aria-label="Copy description"/)
    assert.doesNotMatch(instructions, /Only alphanumeric characters are allowed/)
    assert.doesNotMatch(instructions, /Name it for/)
  }
  const unavailable = renderToStaticMarkup(createElement(ui.PublisherTailscale, { ...props, guidance: { ...guidance, subject: '', subjectError: 'Custom claims require manual review' } }))
  assert.match(unavailable, /Custom claims require manual review/)
  assert.doesNotMatch(unavailable, /repo:Acme\/Website:ref/)
  assert.doesNotMatch(unavailable, /aria-label="Copy subject"/)
  assert.match(html, /<details[^>]*>[\s\S]*Advanced identity guidance/)
  for (const label of ['description', 'issuer URL', 'subject', 'runner tag']) assert.ok(html.includes(`aria-label="Copy ${label}"`))
  assert.equal((html.match(/class="publisher-copy-value"/g) || []).length, 4)
  const customTag = renderToStaticMarkup(createElement(ui.PublisherTailscale, { ...props, tailnet: { ...props.tailnet, tags: 'tag:custom-runner' } }))
  assert.match(customTag, /<code>tag:custom-runner<\/code>/)
})

test('Tailscale requests bind identity discovery and credential saving to the exact selected repository', async () => {
  const calls = []
  const saved = { issuer: 'https://token.actions.githubusercontent.com', subject: 'repo:Acme@42/Website@14:ref:refs/heads/main', tailnet: { clientId: 'selected-client', audience: 'selected-audience', tags: 'tag:selected' } }
  const client = api.createPublisherApi(async (url, options) => { calls.push({ url, ...options }); return Response.json(saved) })
  assert.deepEqual(await client.tailnetGuidance(repository), saved)
  assert.deepEqual(await client.saveTailnet({ repository, ...saved.tailnet }), saved)
  assert.deepEqual(calls.map(call => [call.url, call.method, JSON.parse(call.body)]), [
    ['/api/publishers/tailnet/guidance', 'POST', { repository }],
    ['/api/publishers/tailnet', 'PUT', { repository, ...saved.tailnet }],
  ])
  for (const call of calls) { assert.equal(call.credentials, 'same-origin'); assert.equal(call.cache, 'no-store'); assert.ok(call.signal instanceof AbortSignal) }
})

test('five-step wizard footer keeps GitHub, repository selection, and Tailscale save approvals separate', () => {
  const props = { busy: false, available: true, canAdvance: true, workflowNeeded: false, pendingWorkflow: false, onBack() {}, onAdvance() {}, onCancel() {} }
  for (const [step, label] of [[0, 'Select repository'], [1, 'Set up Tailscale'], [2, 'Save &amp; review']]) {
    const html = renderToStaticMarkup(createElement(ui.PublisherFooter, { ...props, step }))
    assert.ok(html.includes(label), label)
    assert.doesNotMatch(html, /Apply &amp; test push|Open setup PR/)
  }
  const verify = renderToStaticMarkup(createElement(ui.PublisherFooter, { ...props, step: 4 }))
  assert.match(verify, /Back to publishers/)
  assert.doesNotMatch(verify, /class="button primary"/)
})

test('GitHub App permission setup leaves repository access instructions to the Repository step', () => {
  const github = { connected: true, permissionsChecked: true, variablesWrite: true, installUrl: 'https://github.com/apps/services/installations/new' }
  const html = renderToStaticMarkup(createElement(ui.GitHubPermissions, { github, busy: false, onCheck() {} }))
  assert.match(html, /Variables write permission verified/)
  assert.match(html, /Check GitHub permissions/)
  assert.doesNotMatch(html, /Choose repository access on GitHub|Install on selected repositories|Refresh repositories/)
  assert.doesNotMatch(html, /href="https:\/\/github.com\/apps\/services\/installations\/new"/)
})

test('repository access guidance explains installation and safely links the registered app', async () => {
  assert.equal(typeof ui.GitHubRepositoryAccess, 'function')
  const github = { connected: true, permissionsChecked: true, variablesWrite: true, installUrl: 'https://github.com/apps/services/installations/new' }
  const render = (change = {}, busy = false) => renderToStaticMarkup(createElement(ui.GitHubRepositoryAccess, { github: { ...github, ...change }, busy }))
  const html = render()
  for (const text of ['not from your repository list', 'Only select repositories', 'Install', 'Configure', 'Save', 'Refresh repositories', 'organization owner']) assert.ok(html.includes(text), text)
  assert.match(html, /href="https:\/\/github.com\/apps\/services\/installations\/new"/)
  assert.match(html, /target="_blank" rel="noreferrer"/)
  for (const change of [{ installUrl: undefined }, { installUrl: 'https://evil.test/apps/services/installations/new' }]) {
    assert.doesNotMatch(render(change), /href=/)
    assert.match(render(change), /Back.*GitHub App.*Install on selected repositories/)
  }
  assert.doesNotMatch(render({}, true), /href=/)
  assert.doesNotMatch(render({ variablesWrite: false }), /href=/)
  const { readFile } = await import('node:fs/promises')
  const source = await readFile(new URL('./Publishers.tsx', import.meta.url), 'utf8')
  assert.match(source, /step === 1 &&[\s\S]*?<GitHubRepositoryAccess github=\{status\.github\} busy=\{busy\}/)
})

test('GitHub permission rechecks invalidate stale readiness and review before a failed request', async () => {
  assert.equal(typeof api.checkGitHubPermissionsAndRefresh, 'function')
  const current = { available: true, github: { connected: true, permissionsChecked: true, variablesWrite: true, settingsUrl: 'https://github.com/settings/apps/services/permissions' }, tailnet: { clientId: 'client', audience: 'audience', tags: 'tag:runner' }, connections: [] }
  const statuses = [], events = []
  const error = new Error('GitHub unavailable')
  const client = { checkGitHubPermissions: async () => {
    events.push('request')
    assert.equal(api.readyForPublisherRepositories(statuses.at(-1)), false)
    assert.deepEqual(events, ['invalidate', 'clear review', 'request'])
    throw error
  } }
  await assert.rejects(api.checkGitHubPermissionsAndRefresh(client, current, status => { statuses.push(status); events.push('invalidate') }, () => events.push('clear review')), cause => cause === error)
  assert.equal(statuses.length, 1)
  assert.equal(statuses[0].github.settingsUrl, current.github.settingsUrl)
  assert.equal(statuses[0].github.connected, true)
  assert.equal(statuses[0].github.permissionsChecked, false)
  assert.equal(statuses[0].github.variablesWrite, false)
  assert.equal(current.github.variablesWrite, true, 'must not mutate retained snapshots')
  const checked = { ...current, github: { ...current.github, variablesWrite: false } }
  await api.checkGitHubPermissionsAndRefresh({ checkGitHubPermissions: async () => checked }, current, status => statuses.push(status), () => {})
  assert.equal(statuses.at(-1), checked)
})

test('explicit GitHub permission check posts empty authenticated JSON and returns saved status', async () => {
  const calls = []
  const saved = { github: { connected: true, permissionsChecked: true, variablesWrite: false, settingsUrl: 'https://github.com/settings/apps/services/permissions' } }
  const client = api.createPublisherApi(async (url, options) => { calls.push({ url, ...options }); return Response.json(saved) })
  assert.equal(typeof client.checkGitHubPermissions, 'function')
  assert.deepEqual(await client.checkGitHubPermissions(), saved)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, '/api/publishers/github/permissions')
  assert.equal(calls[0].method, 'POST')
  assert.equal(calls[0].body, '{}')
  assert.equal(calls[0].credentials, 'same-origin')
  assert.equal(calls[0].cache, 'no-store')
  assert.equal(calls[0].headers['Content-Type'], 'application/json')
  assert.ok(calls[0].signal instanceof AbortSignal)
  for (const fetcher of [() => new Promise(() => {}), async () => ({ ok: true, json: () => new Promise(() => {}) })]) {
    const bounded = api.createPublisherApi(fetcher, { timeoutMs: 15 })
    await assert.rejects(bounded.checkGitHubPermissions(), error => error.name === 'PublisherRequestTimeout' && error.mutation)
  }
})

test('repository discovery requires GitHub approval but not a Tailscale credential before selection', () => {
  assert.equal(typeof api.githubVariablesReady, 'function')
  assert.equal(typeof api.readyForPublisherRepositories, 'function')
  const github = { connected: true, permissionsChecked: true, variablesWrite: true }
  const saved = { available: true, github, tailnet: { clientId: 'client', audience: 'audience', tags: 'tag:runner' } }
  assert.equal(api.readyForPublisherRepositories(saved), true)
  for (const change of [{ connected: false }, { permissionsChecked: undefined }, { permissionsChecked: false }, { variablesWrite: undefined }, { variablesWrite: false }, { permissionsChecked: 'true' }, { variablesWrite: 1 }]) {
    assert.equal(api.githubVariablesReady({ ...github, ...change }), false)
    assert.equal(api.readyForPublisherRepositories({ ...saved, github: { ...github, ...change } }), false)
  }
  assert.equal(api.readyForPublisherRepositories(null), false)
  assert.equal(api.readyForPublisherRepositories({ ...saved, available: false }), false)
  for (const field of ['clientId', 'audience', 'tags']) assert.equal(api.readyForPublisherRepositories({ ...saved, tailnet: { ...saved.tailnet, [field]: '' } }), true)
  assert.equal(api.readyForPublisherRepositories({ ...saved, tailnet: undefined }), true)
})

test('GitHub App permissions settings links accept only exact personal or organization destinations', () => {
  assert.equal(typeof api.githubPermissionsUrl, 'function')
  for (const url of ['https://github.com/settings/apps/services-publisher/permissions', 'https://github.com/organizations/Acme-Org/settings/apps/services-publisher/permissions']) assert.equal(api.githubPermissionsUrl(url), true, url)
  for (const url of ['', 'javascript:alert(1)', 'http://github.com/settings/apps/services/permissions', 'https://github.com.evil.test/settings/apps/services/permissions', 'https://user@github.com/settings/apps/services/permissions', 'https://github.com:443/settings/apps/services/permissions', 'https://github.com/settings/apps/services/permissions?x=1', 'https://github.com/settings/apps/services/permissions#hash', 'https://github.com/settings/apps/services/permissions?', 'https://github.com/settings/apps/services/permissions#', 'https://github.com/settings/apps/services/permissions/', 'https://github.com/settings/apps/../services/permissions', 'https://github.com/settings/apps/other/../services/permissions', 'https://github.com/settings/apps/%73ervices/permissions', 'https://github.com/organizations/%41cme/settings/apps/services/permissions', 'https://github.com/organizations/Acme/settings/apps/services%2fother/permissions', 'https://github.com/settings/apps/services\\\\permissions', ' https://github.com/settings/apps/services/permissions', 'https://github.com/settings/apps/services/permissions\n']) assert.equal(api.githubPermissionsUrl(url), false, url)
})

test('one-time GitHub Variables step distinguishes unchecked, missing and verified grants before installation', () => {
  assert.equal(typeof ui.GitHubPermissions, 'function')
  const github = { connected: true, installUrl: 'https://github.com/apps/services/installations/new', settingsUrl: 'https://github.com/settings/apps/services/permissions' }
  const render = (change = {}, busy = false) => renderToStaticMarkup(createElement(ui.GitHubPermissions, { github: { ...github, ...change }, busy, onCheck() {} }))
  const unchecked = render()
  for (const text of ['One-time GitHub Variables permission', 'Permissions &amp; events', 'Repository permissions', 'Variables', 'Read and write', 'Save', 'cannot request', 'manifest', 'Configure', 'existing installation', 'Check GitHub permissions', 'not been checked']) assert.ok(unchecked.includes(text), text)
  assert.match(unchecked, /href="https:\/\/github.com\/settings\/apps\/services\/permissions"/)
  assert.doesNotMatch(unchecked, /href="[^"]*installations/)
  assert.match(render({ permissionsChecked: true, variablesWrite: false }), /Variables write permission is missing/)
  assert.match(render({ permissionsChecked: true }), /not been checked/)
  const verified = render({ permissionsChecked: true, variablesWrite: true })
  assert.match(verified, /Variables write permission verified/)
  assert.doesNotMatch(verified, /href="[^"]*installations/)
  assert.doesNotMatch(verified, /Install on selected repositories/)
  assert.match(verified, /installation.*fresh|fresh.*installation/)
  for (const settingsUrl of [undefined, 'https://evil.test/settings/apps/services/permissions', 'https://github.com/settings/apps/services/permissions?redirect=evil']) {
    const html = render({ settingsUrl })
    assert.match(html, /Check permissions to load settings link/)
    assert.doesNotMatch(html, /href=/)
  }
  assert.match(render({ settingsUrl: 'https://github.com/organizations/Acme/settings/apps/services/permissions' }), /href="https:\/\/github.com\/organizations\/Acme\/settings\/apps\/services\/permissions"/)
  assert.match(render({}, true), /<button[^>]*disabled/)
  assert.doesNotMatch(render({}, true), /href=/)
  const calls = []
  const tree = ui.GitHubPermissions({ github, busy: false, onCheck: () => calls.push('check') })
  const elements = node => !node || typeof node !== 'object' ? [] : Array.isArray(node) ? node.flatMap(elements) : [node, ...elements(node.props?.children)]
  assert.deepEqual(calls, [])
  elements(tree).find(node => node.type === 'button' && node.props.children === 'Check GitHub permissions').props.onClick()
  assert.deepEqual(calls, ['check'])
  assert.equal(ui.GitHubPermissions({ github: { connected: false }, busy: false, onCheck() {} }), null)
})

test('registration return exposes pending permission setup on the main page without guessing a link', () => {
  assert.equal(typeof ui.GitHubPermissionNotice, 'function')
  const calls = []
  const props = { github: { connected: true }, busy: false, onFinish: () => calls.push('step0') }
  const html = renderToStaticMarkup(createElement(ui.GitHubPermissionNotice, props))
  assert.match(html, /GitHub App permission setup is pending/)
  assert.match(html, /Finish GitHub permission setup/)
  assert.doesNotMatch(html, /href=/)
  const tree = ui.GitHubPermissionNotice(props)
  tree.props.children.find(node => node.type === 'button').props.onClick()
  assert.deepEqual(calls, ['step0'])
  assert.match(renderToStaticMarkup(createElement(ui.GitHubPermissionNotice, { ...props, busy: true })), /<button[^>]*disabled/)
  for (const github of [{ connected: false }, { connected: true, permissionsChecked: true, variablesWrite: true }]) assert.equal(ui.GitHubPermissionNotice({ ...props, github }), null)
})

test('wizard wires explicit permission checking through the shared lock and invalidates stale repository reviews', async () => {
  const { readFile } = await import('node:fs/promises')
  const source = await readFile(new URL('./Publishers.tsx', import.meta.url), 'utf8')
  assert.match(source, /const checkGitHubPermissions = \(\) => void perform\(async \(\) => \{\s*if \(!status\) return\s*await checkGitHubPermissionsAndRefresh\(api, status, setStatus,/)
  assert.match(source, /checkGitHubPermissionsAndRefresh\(api, status, setStatus, \(\) => \{[^}]*setRepositories\(null\)[^}]*setRepository\(null\)[^}]*setImageRepository\(''\)[^}]*setPlan\(null\)[^}]*setPrUrl\(''\)/)
  assert.match(source, /const readyForRepositories = readyForPublisherRepositories\(status\)/)
  assert.match(source, /<GitHubPermissions github=\{status\.github\} busy=\{busy\} onCheck=\{checkGitHubPermissions\}/)
  assert.match(source, /<GitHubPermissionNotice github=\{status\.github\} busy=\{busy\} onFinish=\{\(\) => changeStep\(0\)\}/)
  assert.match(source, /const loadRepositories = async \(\) => \{\s*if \(!readyForPublisherRepositories\(status\)\) return/)
  assert.match(source, /if \(step === 0\) \{ if \(!readyForPublisherRepositories\(status\)\) return;/)
  assert.equal((source.match(/checkGitHubPermissionsAndRefresh\(api, status, setStatus/g) || []).length, 1, 'only the explicit check action refreshes permission metadata')
})

test('every publisher API operation supplies an abortable bounded request', async () => {
  const signals = []
  const client = api.createPublisherApi(async (_url, options) => { signals.push(options.signal); return Response.json({}) })
  await client.repositories()
  await client.workflow(repository, 'ghcr.io/acme/custom', 'review')
  await client.rotate('saved')
  await client.status()
  assert.ok(signals.every(signal => signal instanceof AbortSignal))
})

test('publisher deadlines bound stalled fetch and body and identify uncertain mutations', { timeout: 1000 }, async () => {
  const stalled = () => new Promise(() => {})
  for (const fetcher of [stalled, async () => ({ ok: true, json: stalled })]) {
    const client = api.createPublisherApi(fetcher, { timeoutMs: 15 })
    await assert.rejects(client.repositories(), error => error.name === 'PublisherRequestTimeout' && !error.mutation)
    await assert.rejects(client.rotate('saved'), error => error.name === 'PublisherRequestTimeout' && error.mutation && /may have applied/.test(error.message))
  }
})

test('publisher caller cancellation settles even if transport ignores its abort signal', { timeout: 1000 }, async () => {
  const caller = new AbortController()
  const client = api.createPublisherApi(() => new Promise(() => {}), { timeoutMs: 200 })
  const pending = client.status(caller.signal)
  caller.abort()
  await assert.rejects(pending, error => error.name === 'AbortError')
})

test('uncertain mutation timeouts refresh saved state once without claiming rollback or hiding errors', async () => {
  const calls = [], statuses = []
  const client = { status: async () => { calls.push('status'); return { connections: [{ state: 'awaiting_callback' }] } } }
  const timeout = new api.PublisherRequestTimeout('POST')
  await api.refreshAfterPublisherTimeout(timeout, client, value => statuses.push(value))
  await api.refreshAfterPublisherTimeout(timeout, client, value => statuses.push(value))
  await api.refreshAfterPublisherTimeout(new api.PublisherRequestTimeout('GET'), client, () => {})
  assert.deepEqual(calls, ['status'])
  assert.equal(statuses[0].connections[0].state, 'awaiting_callback')
  const failed = new api.PublisherRequestTimeout('POST')
  await api.refreshAfterPublisherTimeout(failed, { status: async () => { throw new Error('Offline') } }, () => {})
  assert.equal(failed.stateRefreshed, false)
  assert.match(failed.message, /may have applied/)
})

test('workflow and lifecycle API operations preserve exact payloads and encoded connection IDs', async () => {
  const calls = []
  const client = api.createPublisherApi(async (url, options) => { calls.push({ url, ...options }); return Response.json({}) })
  await client.workflow(repository, 'ghcr.io/acme/custom', 'review-fingerprint')
  await client.disconnect('saved/key')
  await client.rotate('saved/key')
  await client.recover('saved/key')
  assert.deepEqual(calls.map(call => [call.url, call.method, JSON.parse(call.body)]), [
    ['/api/publishers/workflow', 'POST', { repository, imageRepository: 'ghcr.io/acme/custom', fingerprint: 'review-fingerprint' }],
    ['/api/publishers/saved%2Fkey/disconnect', 'POST', {}],
    ['/api/publishers/saved%2Fkey/rotate', 'POST', {}],
    ['/api/publishers/saved%2Fkey/recover', 'POST', {}],
  ])
})

test('Apply sends the reviewed configuration approval unchanged and omits it for legacy plans', async () => {
  const bodies = []
  const client = api.createPublisherApi(async (_url, options) => { bodies.push(JSON.parse(options.body)); return Response.json({}) })
  const approval = 'a'.repeat(64)
  await client.apply(repository, 'ghcr.io/acme/custom', approval)
  await client.apply(repository, 'ghcr.io/acme/custom')
  assert.deepEqual(bodies, [{ repository, imageRepository: 'ghcr.io/acme/custom', approval }, { repository, imageRepository: 'ghcr.io/acme/custom' }])
})

test('Apply always reads saved status after a partial failure and preserves the original failure', async () => {
  const calls = []
  const plan = { repository, imageRepository: 'ghcr.io/acme/custom', approval: 'b'.repeat(64) }
  const failure = new Error('Configuration saved; dispatch failed')
  const saved = { connections: [{ id: 'partial', state: 'error' }] }
  const client = { apply: async (...args) => { calls.push(['apply', ...args]); throw failure }, status: async () => { calls.push(['status']); return saved } }
  const statuses = []
  await assert.rejects(api.applyAndRefresh(client, plan, status => statuses.push(status)), error => error === failure)
  assert.deepEqual(calls, [['apply', repository, plan.imageRepository, plan.approval], ['status']])
  assert.deepEqual(statuses, [saved])
  await assert.rejects(api.applyAndRefresh({ ...client, status: async () => { throw new Error('Offline') } }, plan, () => {}), error => error === failure)
})

test('workflow approval requires a supported fingerprinted plan matching the exact selection', () => {
  const plan = { supported: false, problems: ['Missing hooks'], repository, imageRepository: 'ghcr.io/acme/custom', secretName: 'SERVICES_PUBLISH_TOKEN', variables: {}, workflow: { supported: true, problems: [], fingerprint: 'review', files: [{ path: '.github/workflows/publish.yml', before: '', after: 'notify' }] } }
  assert.equal(api.canOpenWorkflowPlan(plan, repository, plan.imageRepository), true)
  assert.equal(api.canApplyPlan(plan, repository, plan.imageRepository), false)
  assert.equal(api.canApplyPlan({ ...plan, supported: true, problems: [] }, repository, plan.imageRepository), false)
  for (const workflow of [{ ...plan.workflow, supported: false }, { ...plan.workflow, problems: ['Unsafe'] }, { ...plan.workflow, fingerprint: '' }, { ...plan.workflow, files: [] }]) assert.equal(api.canOpenWorkflowPlan({ ...plan, workflow }, repository, plan.imageRepository), false)
  assert.equal(api.canOpenWorkflowPlan(plan, { ...repository, installationId: 999 }, plan.imageRepository), false)
  assert.equal(api.canOpenWorkflowPlan(plan, repository, 'ghcr.io/acme/other'), false)
})

test('PR and workflow run links are HTTPS GitHub URLs bound to the selected repository', () => {
  assert.equal(api.githubPullRequestUrl('https://github.com/Acme/Website/pull/123', repository), true)
  assert.equal(api.githubRunUrl('https://github.com/acme/website/actions/runs/123', repository), true)
  for (const url of ['javascript:alert(1)', 'https://github.com.evil.test/Acme/Website/pull/123', 'https://evil@github.com/Acme/Website/pull/123', 'http://github.com/Acme/Website/pull/123', 'https://github.com/Other/Website/pull/123', 'https://github.com/Acme/Website/pull/123?redirect=evil', 'https://github.com/Acme/Website/pull/123#evil', 'https://github.com/Acme/Website/actions/runs/123']) assert.equal(api.githubPullRequestUrl(url, repository), false, url)
  assert.equal(api.githubRunUrl('https://github.com/Other/Website/actions/runs/123', repository), false)
})

test('supported workflow review escapes complete before/after files and pending PR requires explicit review again', () => {
  const plan = { supported: false, problems: ['Missing hook'], repository, imageRepository: 'ghcr.io/acme/custom', variables: {}, secretName: 'SERVICES_PUBLISH_TOKEN', workflow: { supported: true, problems: [], fingerprint: 'review', files: [{ path: '.github/workflows/<publish>.yml', before: '<script>before()</script>', after: '<script>after()</script>' }] } }
  const html = renderToStaticMarkup(createElement(ui.PublisherReview, { plan }))
  assert.match(html, /Workflow changes need a setup pull request/)
  assert.match(html, /Before/)
  assert.match(html, /After/)
  assert.match(html, /&lt;script&gt;before\(\)&lt;\/script&gt;/)
  assert.match(html, /&lt;script&gt;after\(\)&lt;\/script&gt;/)
  assert.match(html, /&lt;publish&gt;/)
  assert.doesNotMatch(html, /<script>|Automatic workflow editing and setup pull requests are not available/)
  const pending = renderToStaticMarkup(createElement(ui.PublisherWorkflowPending, { plan, prUrl: 'https://github.com/Acme/Website/pull/12' }))
  assert.match(pending, /Merge the setup PR/)
  assert.match(pending, /href="https:\/\/github.com\/Acme\/Website\/pull\/12"/)
  assert.match(pending, /Review again/)
  assert.doesNotMatch(pending, /Apply &amp; test/)
  const unsafe = renderToStaticMarkup(createElement(ui.PublisherWorkflowPending, { plan, prUrl: 'https://github.com/Other/Website/pull/12' }))
  assert.doesNotMatch(unsafe, /href=/)
})

test('publisher API sends same-origin authenticated JSON to the contracted routes only', async () => {
  assert.equal(typeof api.createPublisherApi, 'function')
  const calls = []
  const client = api.createPublisherApi(async (url, options) => { calls.push({ url, ...options }); return Response.json({ available: true }) })
  await client.status()
  await client.startGitHub()
  await client.startGitHub('everyonesoftware')
  await client.saveTailnet({ clientId: 'client', audience: 'audience', tags: 'tag:publisher' })
  await client.repositories()
  await client.plan(repository, 'ghcr.io/acme/custom')
  await client.apply(repository, 'ghcr.io/acme/custom')
  await client.test('saved/key')
  assert.deepEqual(calls.map(({ url, method }) => [url, method]), [
    ['/api/publishers', 'GET'], ['/api/publishers/github/start', 'POST'], ['/api/publishers/github/start', 'POST'], ['/api/publishers/tailnet', 'PUT'],
    ['/api/publishers/repositories', 'GET'], ['/api/publishers/plan', 'POST'], ['/api/publishers', 'POST'], ['/api/publishers/saved%2Fkey/test', 'POST'],
  ])
  for (const call of calls) { assert.equal(call.credentials, 'same-origin'); assert.equal(call.cache, 'no-store'); assert.equal(call.headers['Content-Type'], 'application/json') }
  assert.deepEqual(JSON.parse(calls[1].body), {})
  assert.deepEqual(JSON.parse(calls[2].body), { organization: 'everyonesoftware' })
  assert.deepEqual(JSON.parse(calls[5].body), { repository, imageRepository: 'ghcr.io/acme/custom' })
  assert.deepEqual(JSON.parse(calls[6].body), { repository, imageRepository: 'ghcr.io/acme/custom' })
  assert.deepEqual(JSON.parse(calls[7].body), {})
})

test('publisher errors retain server explanation and unauthorized status', async () => {
  assert.equal(typeof api.createPublisherApi, 'function')
  await assert.rejects(api.createPublisherApi(async () => Response.json({ error: 'Administrator session expired' }, { status: 401 })).status(), error => error.status === 401 && error.message === 'Administrator session expired')
  await assert.rejects(api.createPublisherApi(async () => new Response('Installation unavailable', { status: 503 })).repositories(), /Installation unavailable/)
})

test('exact GHCR validation preserves custom names without normalization', () => {
  assert.equal(typeof api.imageRepositoryProblem, 'function')
  for (const value of ['ghcr.io/acme/custom', 'ghcr.io/acme/my__site', 'ghcr.io/acme/my--site']) assert.equal(api.imageRepositoryProblem(value), '')
  for (const value of ['', 'GHCR.IO/acme/app', 'ghcr.io/Acme/app', ' ghcr.io/acme/app', 'ghcr.io/acme/app ', 'ghcr.io/acme/app:latest', 'ghcr.io/acme/app@sha256:abcd', 'ghcr.io/acme/*', 'docker.io/acme/app', 'ghcr.io/app', 'ghcr.io/acme/' + 'a'.repeat(256)]) assert.match(api.imageRepositoryProblem(value), /exact GHCR/i)
})

test('review gates unsupported plans and stale or changed repository/image selections', () => {
  assert.equal(typeof api.canApplyPlan, 'function')
  const plan = { supported: true, problems: [], repository, imageRepository: 'ghcr.io/acme/custom', variables: { SERVICES_PUBLISH_URL: 'https://example.test/api/images/published' }, secretName: 'SERVICES_PUBLISH_TOKEN' }
  assert.equal(api.canApplyPlan(plan, repository, plan.imageRepository), true)
  for (const altered of [{ ...plan, supported: false }, { ...plan, problems: ['Missing hook'] }, { ...plan, secretName: 'OTHER' }, { ...plan, repository: { ...repository, installationId: 999 } }]) assert.equal(api.canApplyPlan(altered, repository, plan.imageRepository), false)
  assert.equal(api.canApplyPlan(plan, repository, 'ghcr.io/acme/another'), false)
  assert.equal(api.canApplyPlan(null, repository, plan.imageRepository), false)
})

test('only awaiting callback connections trigger local status refresh; verification is not publication', () => {
  assert.equal(typeof api.awaitingCallback, 'function')
  for (const state of ['configured', 'verified', 'error', 'disconnected']) assert.equal(api.awaitingCallback([{ state }]), false)
  assert.equal(api.awaitingCallback([{ state: 'verified' }, { state: 'awaiting_callback' }]), true)
  assert.equal(api.awaitingCallback([]), false)
})

test('GitHub handoff accepts explicit HTTPS GitHub consent and installation destinations only', () => {
  assert.equal(typeof api.githubManifestAction, 'function')
  assert.equal(api.githubManifestAction('https://github.com/settings/apps/new?state=bound-state'), true)
  assert.equal(api.githubManifestAction('https://github.com/organizations/everyonesoftware/settings/apps/new?state=bound-state'), true)
  assert.equal(api.githubManifestAction('https://github.com/organizations/wrong%2Forg/settings/apps/new?state=x'), false)
  for (const value of ['https://github.com/settings/apps/new', 'https://evil.test/settings/apps/new?state=x', 'javascript:alert(1)', 'https://github.com.evil.test/settings/apps/new?state=x', 'http://github.com/settings/apps/new?state=x', 'https://x:secret@github.com/settings/apps/new?state=x']) assert.equal(api.githubManifestAction(value), false)
  assert.equal(api.githubInstallUrl('https://github.com/apps/services/installations/new'), true)
  assert.equal(api.githubInstallUrl('https://evil.test/apps/services/installations/new'), false)
})

test('every page shell shares main navigation and a profile Settings link with one correct current page', async () => {
  assert.equal(typeof ui.Publishers, 'function')
  const { default: App } = await server.ssrLoadModule('/src/App.tsx')
  const { AuthAccounts } = await server.ssrLoadModule('/src/AuthAccounts.tsx')
  const props = { user: { id: 'admin', username: 'admin', role: 'admin' }, onBack() {}, onAccounts() {}, onPublishers() {}, onLogout() {}, onPasswordChanged() {} }
  for (const [component, page] of [[App, 'Monitor'], [App, 'Activity'], [ui.Publishers, 'Publishers'], [AuthAccounts, 'Settings']]) {
    const html = renderToStaticMarkup(createElement(component, { ...props, activePage: page.toLowerCase() }))
    const nav = html.match(/<nav[^>]*aria-label="Main navigation"[^>]*>(.*?)<\/nav>/)?.[1]
    assert.ok(nav, page)
    assert.deepEqual([...nav.matchAll(/>(Monitor|Activity|Publishers|Accounts)</g)].map(match => match[1]), ['Monitor', 'Activity', 'Publishers'])
    const sidebar = html.match(/<aside[^>]*>([\s\S]*?)<\/aside>/)[1]
    assert.equal((sidebar.match(/aria-current="page"/g) || []).length, 1)
    const activeLink = [...sidebar.matchAll(/<a ([^>]+)>(.*?)<\/a>/g)].find(match => match[1].includes('aria-current="page"'))
    assert.ok(activeLink[1].includes(`href="#${page.toLowerCase()}"`))
  }
})

test('GitHub manifest handoff preserves native POST fields and locks only duplicate submissions', () => {
  assert.equal(typeof ui.GitHubManifestForm, 'function')
  for (const destination of ['/settings/apps/new', '/organizations/example/settings/apps/new']) {
    const manifest = { action: `https://github.com${destination}?state=bound-state`, manifest: JSON.stringify({ name: 'Services "publisher" & café', url: 'https://example.test' }) }
    const lock = { current: false }
    const form = ui.GitHubManifestForm({ manifest, lockRef: lock })
    assert.equal(form.type, 'form')
    assert.equal(form.props.action, manifest.action)
    assert.equal(form.props.method, 'post')
    assert.equal(form.props.disabled, undefined)
    const input = form.props.children.find(child => child.type === 'input')
    assert.equal(input.props.type, 'hidden')
    assert.equal(input.props.name, 'manifest')
    assert.equal(input.props.value, manifest.manifest)
    assert.equal(input.props.disabled, undefined)
    let prevented = 0
    const event = { preventDefault() { prevented++ } }
    form.props.onSubmit(event)
    assert.equal(lock.current, true)
    assert.equal(prevented, 0, 'first submission must use native browser serialization')
    form.props.onSubmit(event)
    assert.equal(prevented, 1, 'duplicate submission must be prevented')
    lock.current = false // The page-show handler releases the lock after Back.
    form.props.onSubmit(event)
    assert.equal(lock.current, true)
    assert.equal(prevented, 1, 'returning to Services must permit a fresh handoff')
  }
})

test('GitHub registration explicitly chooses personal or organization ownership', () => {
  assert.equal(typeof ui.GitHubRegistration, 'function')
  const props = { organization: '', onOwner() {}, onOrganization() {}, onSubmit(event) { event.preventDefault() } }
  const personal = renderToStaticMarkup(createElement(ui.GitHubRegistration, { ...props, owner: 'personal' }))
  assert.match(personal, /Register GitHub App under/)
  assert.match(personal, /Personal account/)
  assert.match(personal, /Organization/)
  assert.doesNotMatch(personal, /name="organization"/)
  const organization = renderToStaticMarkup(createElement(ui.GitHubRegistration, { ...props, owner: 'organization' }))
  assert.match(organization, /Organization name/)
  const organizationInput = organization.match(/<input\b[^>]*name="organization"[^>]*>/)?.[0]
  assert.ok(organizationInput)
  assert.match(organizationInput, /\brequired(?:="")?(?:\s|>)/)
  assert.match(organization, /organization owner approval/i)
  assert.match(organization, /private App must be registered to that organization/)
})

test('unsupported workflow review deduplicates reasons and distinguishes adaptation from a broken build', () => {
  const reason = 'No recognized numeric version build argument was found.'
  const plan = { supported: false, problems: [reason], repository, imageRepository: 'ghcr.io/acme/custom', variables: {}, secretName: 'SERVICES_PUBLISH_TOKEN', workflow: { supported: false, problems: [reason], files: [] } }
  const html = renderToStaticMarkup(createElement(ui.PublisherReview, { plan }))
  assert.equal(html.split(reason).length - 1, 1)
  assert.match(html, /Services cannot automatically adapt this workflow/)
  assert.match(html, /does not mean your publishing workflow is broken/)
  assert.doesNotMatch(html, /Merge the setup PR|Add or repair the required workflow/)
})

test('review shows exact configuration and unmet requirements without claiming workflow editing', () => {
  assert.equal(typeof ui.PublisherReview, 'function')
  const plan = { supported: false, problems: ['Existing notification workflow is missing'], repository, imageRepository: 'ghcr.io/acme/custom', variables: { SERVICES_PUBLISH_URL: 'https://private.test/api/images/published', TAILSCALE_CLIENT_ID: 'registered-client' }, secretName: 'SERVICES_PUBLISH_TOKEN' }
  const html = renderToStaticMarkup(createElement(ui.PublisherReview, { plan }))
  for (const text of ['Existing notification workflow is missing', 'ghcr.io/acme/custom', 'SERVICES_PUBLISH_TOKEN', 'TAILSCALE_CLIENT_ID', 'registered-client', 'https://private.test/api/images/published']) assert.ok(html.includes(text), text)
  assert.match(html, /No workflow files are changed/)
  assert.match(html, /not overwritten/)
  assert.match(html, /manually/i)
})

test('server connection states never invent a publication or infer verification from acceptance', () => {
  assert.equal(typeof ui.PublisherConnection, 'function')
  const connection = { id: 'connection', repository, imageRepository: 'ghcr.io/acme/custom', createdAt: '2026-01-01T00:00:00Z' }
  for (const [state, label] of [['configured', 'Configured'], ['awaiting_callback', 'Waiting for authenticated callback'], ['verified', 'Connectivity verified'], ['error', 'Connection needs attention'], ['disconnected', 'Disconnected']]) {
    const html = renderToStaticMarkup(createElement(ui.PublisherConnection, { connection: { ...connection, state, error: state === 'error' ? 'Runner cannot reach HTTPS endpoint' : undefined }, busy: false, onTest() {} }))
    assert.ok(html.includes(label), label)
    assert.doesNotMatch(html, /Receiving publications|Latest publication|Publication received/)
    if (state === 'verified') assert.match(html, /not a successful publication/)
    if (state === 'error') assert.match(html, /Runner cannot reach HTTPS endpoint/)
  }
})

test('verified connections render only actual publication metadata and a repository-bound run link', () => {
  const publication = { version: '<release>', digest: 'sha256:' + 'a'.repeat(64), tags: ['latest', 'release'], commit: 'abc123', runUrl: 'https://github.com/Acme/Website/actions/runs/42', publishedAt: '2026-01-01T00:00:00Z', receivedAt: '2026-01-01T00:01:00Z', result: 'accepted' }
  const connection = { id: 'saved', state: 'verified', repository, imageRepository: 'ghcr.io/acme/custom', createdAt: publication.publishedAt, lastPublication: publication }
  const render = next => renderToStaticMarkup(createElement(ui.PublisherConnection, { connection: next, busy: false, onTest() {}, view: 'publication' }))
  const html = render(connection)
  for (const text of ['Latest publication', '&lt;release&gt;', publication.digest, 'latest', 'release', 'abc123', 'accepted', 'Published', 'Received']) assert.ok(html.includes(text), text)
  assert.match(html, /href="https:\/\/github.com\/Acme\/Website\/actions\/runs\/42"/)
  assert.doesNotMatch(html, /<release>/)
  assert.doesNotMatch(render({ ...connection, lastPublication: { ...publication, runUrl: 'javascript:alert(1)' } }), /href="javascript:/)
  assert.doesNotMatch(render({ ...connection, lastPublication: { ...publication, runUrl: 'https://github.com/Other/Website/actions/runs/42' } }), /href=/)
  for (const lastPublication of [undefined, { ...publication, digest: '' }, { ...publication, receivedAt: '' }]) assert.doesNotMatch(render({ ...connection, lastPublication }), /Latest publication|Receiving publications/)
  for (const state of ['configured', 'awaiting_callback', 'error', 'disconnected']) assert.doesNotMatch(render({ ...connection, state }), /Latest publication|Receiving publications/)
})

test('connection management exposes recovery and reconnect but leaves deletion to the row wizard', () => {
  const connection = { id: 'saved', state: 'error', repository, imageRepository: 'ghcr.io/acme/custom', createdAt: '2026-01-01T00:00:00Z' }
  const props = { busy: false, onTest() {}, onRecover() {}, onManage() {} }
  const error = renderToStaticMarkup(createElement(ui.PublisherConnection, { ...props, connection }))
  assert.match(error, /Repair &amp; retry push/)
  assert.match(error, /saved setup.*no duplicate key/i)
  assert.match(error, />Disconnect</)
  const verified = renderToStaticMarkup(createElement(ui.PublisherConnection, { ...props, connection: { ...connection, state: 'verified' } }))
  assert.match(verified, />Rotate key</)
  assert.doesNotMatch(verified, /Repair &amp; retry/)
  const disconnected = renderToStaticMarkup(createElement(ui.PublisherConnection, { ...props, connection: { ...connection, state: 'disconnected' } }))
  assert.match(disconnected, />Reconnect</)
  assert.doesNotMatch(disconnected, />Remove</)
  assert.doesNotMatch(disconnected, />Disconnect<|>Rotate key<|Retry push test/)
})

test('disconnected publisher with pending remote disable retains a targeted cleanup retry', () => {
  const calls = []
  const connection = { id: 'saved', state: 'disconnected', repository, imageRepository: 'ghcr.io/acme/custom', createdAt: '', remoteDisablePending: true, error: 'GitHub disable could not be verified' }
  const props = { connection, busy: false, onTest: () => calls.push('test'), onManage: (target, action) => calls.push([target.id, action]) }
  const html = renderToStaticMarkup(createElement(ui.PublisherConnection, props))
  assert.match(html, /Retry GitHub cleanup/)
  assert.doesNotMatch(html, /Rotate key|Retry push test/)
  const elements = node => !node || typeof node !== 'object' ? [] : Array.isArray(node) ? node.flatMap(elements) : [node, ...elements(node.props?.children)]
  const button = elements(ui.PublisherConnection(props)).find(node => node.type === 'button' && node.props.children === 'Retry GitHub cleanup')
  button.props.onClick()
  assert.deepEqual(calls, [['saved', 'disconnect']])
})

test('disconnected buttons target their own connection and respect busy state', () => {
  const calls = []
  const connection = { id: 'old/key', state: 'disconnected', repository, imageRepository: 'ghcr.io/acme/custom', createdAt: '' }
  const elements = node => !node || typeof node !== 'object' ? [] : Array.isArray(node) ? node.flatMap(elements) : [node, ...elements(node.props?.children)]
  const props = { connection, busy: false, onTest() {}, onManage: (target, action) => calls.push([target.id, action]) }
  const buttons = elements(ui.PublisherConnection(props)).filter(node => node.type === 'button')
  assert.deepEqual(buttons.map(node => node.props.children), ['Reconnect'])
  assert.deepEqual(calls, [])
  buttons.forEach(button => button.props.onClick())
  assert.deepEqual(calls, [['old/key', 'reconnect']])
  assert.ok(elements(ui.PublisherConnection({ ...props, busy: true })).filter(node => node.type === 'button').every(node => node.props.disabled))
})

test('publisher ordering stably places all disconnected entries last without mutating saved state', () => {
  const connections = ['disconnected', 'verified', 'error', 'disconnected', 'awaiting_callback', 'configured'].map((state, id) => ({ id, state }))
  assert.equal(typeof api.orderPublisherConnections, 'function')
  assert.deepEqual(api.orderPublisherConnections(connections).map(item => item.id), [1, 2, 4, 5, 0, 3])
  assert.deepEqual(connections.map(item => item.id), [0, 1, 2, 3, 4, 5])
})

test('reconnect and remove API calls use encoded targets and explicit mutation methods', async () => {
  const calls = []
  const client = api.createPublisherApi(async (url, options) => { calls.push([url, options.method, options.body]); return Response.json({ removed: true }) })
  await client.reconnect('old/key')
  await client.remove('old/key')
  assert.deepEqual(calls, [['/api/publishers/old%2Fkey/reconnect', 'POST', '{}'], ['/api/publishers/old%2Fkey', 'DELETE', '{}']])
})

test('reconnect confirmation explains new credentials and explicit approval', () => {
  const connection = { id: 'saved', state: 'disconnected', repository, imageRepository: 'ghcr.io/acme/custom', createdAt: '' }
  for (const [action, label] of [['reconnect', 'Reconnect &amp; test push']]) {
    const html = renderToStaticMarkup(createElement(ui.PublisherConfirmation, { connection, action, busy: false, onCancel() {}, onConfirm() {} }))
    assert.ok(html.includes(label))
    assert.ok(html.indexOf('>Cancel<') < html.indexOf(`>${label}<`))
    assert.match(html, /fresh credential/i)
    assert.match(html, /authenticated callback/i)
    assert.match(html, /replace.*GitHub.*secret/i)
  }
})

test('manual Tailscale attestation hides activation actions but retains emergency disconnect and cleanup retry', () => {
  const props = { busy: false, onTest() {}, onRecover() {}, onManage() {} }
  for (const deletion of [{ prUrl: '', workflowVerified: true, tailscaleConfirmed: true }, { prUrl: '', workflowVerified: false, tailscaleConfirmed: false, tailscaleRevocationAttested: true }]) {
    for (const state of ['verified', 'error', 'configured', 'awaiting_callback', 'disconnected']) {
      const html = renderToStaticMarkup(createElement(ui.PublisherConnection, { ...props, connection: { id: 'saved', repository, imageRepository: 'ghcr.io/acme/custom', state, createdAt: '', remoteDisablePending: true, deletion } }))
      assert.doesNotMatch(html, />Test push<|>Rotate key<|>Reconnect<|>Repair &amp; retry push<|>Retry push test</)
      assert.match(html, /Manual Tailscale confirmation is saved/)
      assert.match(html, state === 'disconnected' ? />Retry GitHub cleanup</ : />Disconnect</)
    }
  }
})

test('named compact confirmations explain owned resources, callback-safe rotation, and default cancellation', () => {
  const connection = { id: 'saved', state: 'verified', repository, imageRepository: 'ghcr.io/acme/custom', createdAt: '' }
  for (const action of ['disconnect', 'rotate']) {
    const html = renderToStaticMarkup(createElement(ui.PublisherConfirmation, { connection, action, busy: false, onCancel() {}, onConfirm() {} }))
    assert.match(html, /<dialog[^>]*class="publisher-confirmation"/)
    assert.match(html, /Acme\/Website/)
    assert.match(html, /ghcr.io\/acme\/custom/)
    assert.match(html, />Cancel</)
    assert.ok(html.indexOf('>Cancel<') < html.indexOf(action === 'disconnect' ? '>Disconnect publisher<' : '>Rotate &amp; test push<'))
    if (action === 'disconnect') { assert.match(html, /revoke/i); assert.match(html, /owned notification configuration/); assert.match(html, /Shared Tailscale.*not.*deleted/) }
    else { assert.match(html, /old key stays active until.*authenticated callback/i); assert.match(html, /accepted dispatch is not success/i) }
  }
})

test('connection buttons invoke only their explicit target action, without dispatching on render', () => {
  const calls = []
  const connection = { id: 'saved/key', state: 'verified', repository, imageRepository: 'ghcr.io/acme/custom', createdAt: '' }
  const elements = node => !node || typeof node !== 'object' ? [] : Array.isArray(node) ? node.flatMap(elements) : [node, ...elements(node.props?.children)]
  const props = { connection, busy: false, onTest: id => calls.push(['test', id]), onRecover: id => calls.push(['recover', id]), onManage: (next, action) => calls.push([action, next.id]) }
  const tree = ui.PublisherConnection(props)
  assert.deepEqual(calls, [])
  const buttons = elements(tree).filter(node => node.type === 'button')
  assert.deepEqual(buttons.map(node => node.props.children), ['Test push', 'Rotate key', 'Disconnect'])
  for (const button of buttons) button.props.onClick()
  assert.deepEqual(calls, [['test', 'saved/key'], ['rotate', 'saved/key'], ['disconnect', 'saved/key']])
  const recovery = elements(ui.PublisherConnection({ ...props, connection: { ...connection, state: 'error' } })).find(node => node.type === 'button' && node.props.children === 'Repair & retry push')
  recovery.props.onClick()
  assert.deepEqual(calls.at(-1), ['recover', 'saved/key'])
  const pending = elements(ui.PublisherConnection({ ...props, connection: { ...connection, state: 'awaiting_callback' } })).filter(node => node.type === 'button')
  assert.deepEqual(pending.map(node => node.props.children), ['Disconnect'])
})

test('failed management requests retain a visible error inside the confirmation', () => {
  const html = renderToStaticMarkup(createElement(ui.PublisherConfirmation, { connection: { id: 'saved', repository, imageRepository: 'ghcr.io/acme/custom' }, action: 'disconnect', busy: false, error: 'GitHub configuration unavailable', onCancel() {}, onConfirm() {} }))
  assert.match(html, /role="alert"[^>]*>GitHub configuration unavailable/)
  assert.match(html, />Cancel</)
})

test('workflow diff and compact confirmation styling preserve the responsive warm surface and shared footer', async () => {
  const { readFile } = await import('node:fs/promises')
  const css = await readFile(new URL('./Publishers.css', import.meta.url), 'utf8')
  assert.match(css, /\.publisher-diff\s*\{[^}]*grid-template-columns:/)
  assert.match(css, /\.publisher-confirmation\s*\{[^}]*max-width:\s*520px/)
  assert.match(css, /\.publisher-confirmation\s*\{[^}]*background:\s*var\(--surface\)/)
  assert.match(css, /@media[^}]*760px[\s\S]*\.publisher-diff[^}]*grid-template-columns:\s*minmax\(0, 1fr\)/)
})

test('shared wizard footer keeps explicit PR, review-again and apply approvals distinct and gated', () => {
  const calls = []
  const props = { step: 3, busy: false, available: true, canAdvance: true, workflowNeeded: false, pendingWorkflow: false, onBack: () => calls.push('back'), onAdvance: () => calls.push('advance'), onCancel: () => calls.push('cancel') }
  for (const [state, label] of [[{}, 'Apply &amp; test push'], [{ workflowNeeded: true }, 'Open setup PR'], [{ workflowNeeded: true, pendingWorkflow: true }, 'Review again']]) {
    const html = renderToStaticMarkup(createElement(ui.PublisherFooter, { ...props, ...state }))
    assert.match(html, /<footer class="publisher-footer">/)
    assert.ok(html.includes(label), label)
    assert.match(html, />Back</)
    assert.match(html, /Cancel setup/)
    assert.equal((html.match(/class="button primary"/g) || []).length, 1)
  }
  for (const state of [{ canAdvance: false }, { busy: true }, { available: false }]) {
    const html = renderToStaticMarkup(createElement(ui.PublisherFooter, { ...props, ...state }))
    const primary = html.match(/<button[^>]*class="button primary"[^>]*>/)?.[0]
    assert.match(primary, /\bdisabled/)
  }
  const tree = ui.PublisherFooter(props)
  const children = tree.props.children.flat().filter(Boolean)
  children.find(node => node.props?.children === 'Back').props.onClick()
  children.find(node => node.props?.children === 'Cancel setup').props.onClick()
  assert.deepEqual(calls, ['back', 'cancel'])
  children.find(node => node.props?.className === 'button primary').props.onClick()
  assert.deepEqual(calls, ['back', 'cancel', 'advance'])
})
