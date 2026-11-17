// Local browser fixtures only. No external mutations.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ ...(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : { channel: 'chromium' }), headless: true })
const page = await browser.newPage({ viewport: { width: 1200, height: 1000 } })
page.setDefaultTimeout(5000)
const errors = []
page.on('pageerror', error => errors.push(error.message))
const connection = (id, state) => ({ id, state, repository: { id: Number(id), fullName: `Demo/Publisher-${id}`, defaultBranch: 'main', installationId: 8 }, imageRepository: `ghcr.io/demo/publisher-${id}`, createdAt: '2026-01-01T00:00:00Z' })
const status = { available: true, github: { connected: true, permissionsChecked: true, variablesWrite: true }, tailnet: { clientId: '', audience: '', tags: '' }, connections: [connection('1', 'verified'), connection('2', 'disconnected')], endpoint: 'https://services.example.test/api/images/published' }
const plans = Object.fromEntries(['1', '2'].map(id => [id, { workflow: { supported: true, problems: null, fingerprint: `review-${id}`, files: [{ path: '.github/workflows/publish.yml', before: 'build: keep\nnotify: remove <script>', after: 'build: keep' }] }, prUrl: '', workflowVerified: false, tailscaleConfirmed: false, tailscale: { clientId: `saved-client-${id}`, audience: `saved-audience-${id}`, tags: 'tag:publisher', shared: id === '2', credentialsUrl: 'https://evil.test/untrusted' } }]))
const calls = []
for (const [id, plan] of Object.entries(plans)) plan.tailscaleReview = `tailnet-review-${id}`
let failVerify = true
let failAttestation = true
let disablePending = true
let failRemove = true
let failPlan = false
await page.route('**/api/**', async route => {
  const req = route.request(), path = new URL(req.url()).pathname
  if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
  if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'admin', username: 'Browser fixture', role: 'admin' } } })
  if (path === '/api/publishers' && req.method() === 'GET') { calls.push('status'); return route.fulfill({ json: status }) }
  const deletion = path.match(/^\/api\/publishers\/(\d)\/deletion\/(plan|workflow|verify|tailscale)$/)
  if (deletion) {
    const [, id, action] = deletion
    assert.equal(req.method(), 'POST')
    calls.push(`${id}:${action}`)
    if (action === 'plan' && failPlan) return route.fulfill({ status: 503, body: 'GitHub cannot read the current default branch. Retry the saved plan.' })
    if (action === 'plan') {
      const saved = status.connections.find(c => c.id === id)
      saved.deletion = { ...saved.deletion, prUrl: plans[id].prUrl, workflowVerified: plans[id].workflowVerified, tailscaleConfirmed: plans[id].tailscaleConfirmed }
    }
    if (action === 'workflow') {
      assert.deepEqual(req.postDataJSON(), { fingerprint: `review-${id}` })
      plans[id].prUrl = `https://github.com/Demo/Publisher-${id}/pull/42`
      status.connections.find(c => c.id === id).deletion = { prUrl: plans[id].prUrl, workflowVerified: false, tailscaleConfirmed: false }
    }
    if (action === 'verify') {
      assert.deepEqual(req.postDataJSON(), {})
      if (failVerify) return route.fulfill({ status: 409, body: 'Workflow integration is still present. Merge the cleanup PR first.' })
      plans[id].workflowVerified = true
      status.connections.find(c => c.id === id).deletion.workflowVerified = true
    }
    if (action === 'tailscale') {
      assert.deepEqual(req.postDataJSON(), { confirmed: true, clientId: `saved-client-${id}`, scope: id === '2' ? 'repository' : 'dedicated', reviewFingerprint: plans[id].tailscaleReview })
      if (failAttestation) return route.fulfill({ status: 409, body: 'Saved Tailscale configuration changed; review again.' })
      plans[id].tailscaleConfirmed = true
      status.connections.find(c => c.id === id).deletion.tailscaleConfirmed = true
      status.connections.find(c => c.id === id).deletion.tailscaleRevocationAttested = true
    }
    return route.fulfill({ json: plans[id] })
  }
  const lifecycle = path.match(/^\/api\/publishers\/(\d)(\/disconnect)?$/)
  if (lifecycle) {
    const [, id, disconnect] = lifecycle
    const saved = status.connections.find(c => c.id === id)
    if (disconnect) {
      assert.equal(req.method(), 'POST'); assert.deepEqual(req.postDataJSON(), {})
      calls.push(`${id}:disconnect`)
      saved.state = 'disconnected'; saved.remoteDisablePending = disablePending
      return route.fulfill({ json: { connection: saved } })
    }
    assert.equal(req.method(), 'DELETE'); calls.push(`${id}:remove`)
    if (failRemove) return route.fulfill({ status: 409, body: 'Successor publisher owns this configuration. Cleanup forbidden.' })
    status.connections = status.connections.filter(c => c.id !== id)
    return route.fulfill({ json: { removed: true } })
  }
  if (path === '/api/projects') return route.fulfill({ json: { projects: [] } })
  if (path === '/api/monitor') return route.fulfill({ json: { connected: true, incidents: [], events: [] } })
  if (path === '/api/operations') return route.fulfill({ json: { operations: [] } })
  if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
  throw new Error(`Unexpected request ${req.method()} ${path}`)
})
const card = id => page.getByRole('article', { name: `Publisher Demo/Publisher-${id}`, exact: true })
const dialog = () => page.getByRole('dialog')
try {
  await page.goto(`${server.resolvedUrls.local[0]}#publishers`)
  for (const id of ['1', '2']) {
    await card(id).waitFor()
    const button = card(id).getByRole('button', { name: 'Delete', exact: true })
    assert.equal(await button.count(), 1, 'connected and disconnected rows expose Delete')
    assert.equal(await card(id).locator('.publisher-row-summary button').count(), 0, 'Delete is never nested in the summary button')
    await button.click()
    await dialog().getByRole('heading', { name: 'Remove workflow integration', exact: true }).waitFor()
    await dialog().getByText('notify: remove <script>', { exact: false }).waitFor()
    assert.equal(await dialog().locator('script').count(), 0)
    assert.equal(await dialog().getByRole('button', { name: 'Open cleanup PR', exact: true }).isEnabled(), true)
    await dialog().getByRole('button', { name: 'Close — finish later', exact: true }).click()
  }
  await card('1').getByRole('button', { name: 'Continue deletion', exact: true }).click()
  await dialog().getByRole('button', { name: 'Open cleanup PR', exact: true }).click()
  await dialog().getByRole('heading', { name: 'Merge & verify cleanup', exact: true }).waitFor()
  assert.equal(await dialog().getByRole('link', { name: 'Review cleanup pull request ↗' }).getAttribute('href'), 'https://github.com/Demo/Publisher-1/pull/42')
  assert.equal(await card('1').getByText('Connectivity verified', { exact: true }).count(), 1, 'opening a PR must not disconnect')
  await dialog().getByRole('button', { name: 'Verify workflow cleanup', exact: true }).click()
  await dialog().getByRole('alert').getByText('Workflow integration is still present. Merge the cleanup PR first.', { exact: true }).waitFor()
  assert.equal(await dialog().getByRole('heading', { name: 'Merge & verify cleanup', exact: true }).count(), 1)
  assert.deepEqual(calls.slice(-2), ['1:plan', 'status'], 'failure reloads current plan and then reads its saved status')
  await dialog().getByRole('button', { name: 'Close — finish later', exact: true }).click()
  await page.reload()
  await card('1').getByRole('button', { name: 'Continue deletion', exact: true }).click()
  await dialog().getByRole('heading', { name: 'Merge & verify cleanup', exact: true }).waitFor()
  failVerify = false
  await dialog().getByRole('button', { name: 'Verify workflow cleanup', exact: true }).click()
  await dialog().getByRole('heading', { name: 'Revoke Tailscale credentials', exact: true }).waitFor()
  for (const text of ['saved-client-1', 'saved-audience-1', 'tag:publisher', 'manual attestation', 'no Tailscale administrator credentials', 'Local reuse detection cannot rule out external users']) await dialog().getByText(text, { exact: false }).first().waitFor()
  const link = dialog().getByRole('link', { name: 'Open Tailscale credentials ↗' })
  assert.equal(await link.getAttribute('href'), 'https://console.tailscale.com/admin/settings/trust-credentials')
  assert.equal(await link.getAttribute('target'), '_blank')
  assert.match(await link.getAttribute('rel'), /noopener/)
  await page.context().route('https://console.tailscale.com/**', route => route.fulfill({ body: '<h1>Local fixture for new-tab navigation</h1>', contentType: 'text/html' }))
  const popupPromise = page.waitForEvent('popup')
  await link.click()
  const popup = await popupPromise
  await popup.waitForLoadState()
  assert.equal(popup.url(), 'https://console.tailscale.com/admin/settings/trust-credentials')
  await popup.close()
  const attest = () => dialog().getByRole('button', { name: 'Save manual confirmation', exact: true })
  assert.equal(await attest().isEnabled(), false)
  await dialog().getByLabel('Credential scope', { exact: true }).selectOption('dedicated')
  await dialog().getByLabel('Confirm saved Client ID', { exact: true }).fill('wrong-client')
  await dialog().getByRole('checkbox', { name: 'I manually removed this publisher’s Tailscale access in the scope selected above.' }).check()
  assert.equal(await attest().isEnabled(), false)
  await dialog().getByLabel('Confirm saved Client ID', { exact: true }).fill('saved-client-1')
  await dialog().getByRole('checkbox').check()
  plans['1'].tailscale.tags = 'tag:changed-after-review'
  await attest().click()
  await dialog().getByRole('alert').getByText('Tailscale settings or workflow changed. Review the refreshed plan and confirm the exact saved access again.', { exact: true }).waitFor()
  assert.equal(calls.includes('1:tailscale'), false, 'a changed audience/tags/scope must not receive the old manual approval')
  await dialog().getByText('tag:changed-after-review', { exact: true }).waitFor()
  assert.equal(await attest().isEnabled(), false)
  plans['1'].tailscaleReview = 'new-opaque-review-1'
  await dialog().getByRole('checkbox').check()
  await attest().click()
  await dialog().getByRole('alert').getByText('Tailscale settings or workflow changed. Review the refreshed plan and confirm the exact saved access again.', { exact: true }).waitFor()
  assert.equal(calls.includes('1:tailscale'), false, 'a stale opaque review must not be silently replaced when posting approval')
  await dialog().getByRole('checkbox').check()
  await attest().click()
  await dialog().getByRole('alert').getByText('Saved Tailscale configuration changed; review again.', { exact: true }).waitFor()
  assert.equal(await attest().isEnabled(), false, 'failed manual attestation requires renewed confirmation')
  failAttestation = false
  await dialog().getByRole('checkbox').check()
  await attest().click()
  await dialog().getByRole('heading', { name: 'Delete publisher?', exact: true }).waitFor()
  assert.equal(await card('1').getByText('Connectivity verified', { exact: true }).count(), 1, 'manual attestation must not disconnect')
  await dialog().getByRole('button', { name: 'Close — finish later', exact: true }).click()
  plans['2'].workflowVerified = true
  status.connections.find(c => c.id === '2').deletion = { prUrl: '', workflowVerified: true, tailscaleConfirmed: false }
  await page.reload()
  await card('2').getByRole('button', { name: 'Continue deletion', exact: true }).click()
  await dialog().getByRole('heading', { name: 'Revoke Tailscale credentials', exact: true }).waitFor()
  await dialog().getByText('Do not revoke the shared credential.', { exact: false }).waitFor()
  assert.ok((await dialog().boundingBox()).width > 700, 'desktop deletion review must use its wider layout rather than the compact confirmation width')
  if (process.env.DELETION_DESKTOP_SCREENSHOT) await page.screenshot({ path: process.env.DELETION_DESKTOP_SCREENSHOT, fullPage: true })
  await page.setViewportSize({ width: 390, height: 800 })
  assert.ok(await dialog().evaluate(el => el.scrollWidth <= el.clientWidth), 'mobile Tailscale controls must not overflow')
  if (process.env.DELETION_SCREENSHOT) await page.screenshot({ path: process.env.DELETION_SCREENSHOT, fullPage: true })
  assert.equal(await dialog().getByLabel('Credential scope', { exact: true }).locator('option[value="dedicated"]').isDisabled(), true)
  await dialog().getByLabel('Credential scope', { exact: true }).selectOption('repository')
  await dialog().getByLabel('Confirm saved Client ID', { exact: true }).fill('saved-client-2')
  await dialog().getByRole('checkbox').check()
  await attest().click()
  await dialog().getByRole('heading', { name: 'Delete publisher?', exact: true }).waitFor()
  await dialog().getByRole('button', { name: 'Close — finish later', exact: true }).click()
  await page.reload()
  await card('1').getByRole('button', { name: 'Continue deletion', exact: true }).click()
  await dialog().getByRole('heading', { name: 'Delete publisher?', exact: true }).waitFor()
  const finish = () => dialog().getByRole('button', { name: 'Disconnect & delete', exact: true })
  assert.equal(await finish().isEnabled(), false, 'final destructive action requires renewed approval')
  await dialog().getByRole('checkbox', { name: 'I confirm disconnecting and deleting this publisher from Services.' }).check()
  await finish().click()
  await dialog().getByRole('alert').getByText('GitHub disable is not confirmed. The publisher is retained; retry cleanup before deleting.', { exact: true }).waitFor()
  assert.equal(calls.includes('1:remove'), false, 'pending GitHub disable must never reach DELETE')
  assert.equal(await card('1').count(), 1)
  disablePending = false
  await dialog().getByRole('checkbox').check()
  await finish().click()
  await dialog().getByRole('alert').getByText('Successor publisher owns this configuration. Cleanup forbidden.', { exact: true }).waitFor()
  assert.equal(await card('1').count(), 1, 'server-forbidden cleanup must retain record')
  await dialog().getByRole('button', { name: 'Close — finish later', exact: true }).click()
  await page.setViewportSize({ width: 390, height: 800 })
  await page.reload()
  await card('1').getByRole('button', { name: 'Continue deletion', exact: true }).click()
  await dialog().getByRole('heading', { name: 'Delete publisher?', exact: true }).waitFor()
  assert.equal(await finish().isEnabled(), false)
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'mobile rows must not overflow')
  assert.ok(await dialog().evaluate(el => el.scrollWidth <= el.clientWidth), 'mobile deletion dialog must not overflow')
  failRemove = false
  await dialog().getByRole('checkbox').check()
  await finish().click()
  await card('1').waitFor({ state: 'detached' })
  await dialog().waitFor({ state: 'detached' })
  await page.reload()
  await card('2').waitFor()
  assert.equal(await card('1').count(), 0, 'success is persisted in read-back status')
  const lifecycleCalls = calls.filter(call => /:(disconnect|remove)$/.test(call))
  assert.deepEqual(lifecycleCalls, ['1:disconnect', '1:disconnect', '1:remove', '1:disconnect', '1:remove'])
  const remaining = status.connections.find(c => c.id === '2')
  plans['2'].workflowVerified = false; plans['2'].tailscaleConfirmed = false
  plans['2'].prUrl = 'https://github.com/Other/Repository/pull/42'
  remaining.deletion = { ...remaining.deletion, prUrl: plans['2'].prUrl, workflowVerified: false, tailscaleConfirmed: false }
  await card('2').getByRole('button', { name: 'Continue deletion', exact: true }).click()
  await dialog().getByRole('alert').getByText('The cleanup PR link could not be verified for this repository. No external link was opened.', { exact: true }).waitFor()
  assert.equal(await dialog().getByRole('link').count(), 0, 'cross-repository PR links must not be exposed')
  await dialog().getByRole('button', { name: 'Close — finish later', exact: true }).click()
  plans['2'].prUrl = ''; plans['2'].workflow = { supported: false, problems: ['Shared workflow ownership requires manual review.'], files: null, fingerprint: '' }
  await card('2').getByRole('button', { name: 'Continue deletion', exact: true }).click()
  await dialog().getByRole('alert').getByText('Shared workflow ownership requires manual review.', { exact: true }).waitFor()
  assert.equal(await dialog().getByRole('button', { name: 'Open cleanup PR', exact: true }).isEnabled(), false)
  assert.doesNotMatch(await dialog().innerText(), /demo controls|prototype controls|simulate|reset demo/i)
  await dialog().getByRole('button', { name: 'Close — finish later', exact: true }).click()
  failPlan = true
  await card('2').getByRole('button', { name: 'Continue deletion', exact: true }).click()
  await dialog().getByRole('alert').getByText('GitHub cannot read the current default branch. Retry the saved plan.', { exact: true }).waitFor()
  assert.equal(await dialog().getByRole('button', { name: 'Disconnect & delete', exact: true }).count(), 0, 'failed current plan never reuses saved proof')
  failPlan = false; plans['2'].workflowVerified = true; plans['2'].workflow.supported = true; plans['2'].workflow.problems = null
  await dialog().getByRole('button', { name: 'Refresh plan', exact: true }).click()
  await dialog().getByRole('heading', { name: 'Revoke Tailscale credentials', exact: true }).waitFor()
  assert.equal(await dialog().getByRole('button', { name: 'Save manual confirmation', exact: true }).isEnabled(), false, 'server verification of manual workflow removal skips PR, not attestation')
  await dialog().getByRole('button', { name: 'Close — finish later', exact: true }).click()
  await card('2').locator('.publisher-row-summary').click()
  await card('2').getByRole('tab', { name: 'Connection', exact: true }).click()
  assert.equal(await card('2').getByRole('button', { name: 'Reconnect', exact: true }).count(), 0, 'sticky attestation blocks activation even after current proof is invalidated')
  await card('2').getByText('Manual Tailscale confirmation is saved.', { exact: false }).waitFor()
  plans['2'].tailscaleReview = ''
  await card('2').getByRole('button', { name: 'Continue deletion', exact: true }).click()
  await dialog().getByRole('heading', { name: 'Revoke Tailscale credentials', exact: true }).waitFor()
  await dialog().getByLabel('Credential scope', { exact: true }).selectOption('repository')
  await dialog().getByLabel('Confirm saved Client ID', { exact: true }).fill('saved-client-2')
  await dialog().getByRole('checkbox').check()
  assert.equal(await attest().isEnabled(), false, 'missing review fingerprint blocks manual approval')
  assert.deepEqual(errors, [])
  console.log('PASS publisher deletion browser: connected/disconnected Delete; diff; PR/verify/resume; manual attestation gating/failure/config refresh; opaque review fingerprint; sticky revocation activation block; trusted new-tab link; dedicated/shared safeguards; final gating; pending disable blocks DELETE; successor error; cleanup retry/resume; mobile; persisted success; unsafe PR/unsupported/null plans; read failure/retry; manual workflow removal; no page errors')
} finally { await browser.close(); await server.close() }
