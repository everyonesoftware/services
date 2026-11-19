// Rendered application with isolated HTTP fixtures; no external mutations.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || '/opt/data/.local/bin/chromium', headless: true })
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
page.setDefaultTimeout(5000)
const errors = []; page.on('pageerror', error => errors.push(error.message))
const repository = { id: 14, installationId: 8, fullName: 'everyonesoftware/finance', defaultBranch: 'main' }
const reconciliation = { state: 'review_required', previousName: 'everyonesoftware/finance-typescript', currentName: repository.fullName, repositoryId: 14, installationId: 8, message: 'Review the historical image binding.' }
const connection = { id: 'exact publisher/id', repository, imageRepository: 'ghcr.io/everyonesoftware/finance', state: 'verified', createdAt: '2026-11-18T12:00:00Z', reconciliation: { ...reconciliation } }
const status = { available: true, github: { connected: true, permissionsChecked: true, variablesWrite: true }, tailnet: {}, endpoint: '', connections: [...Array.from({ length: 7 }, (_, index) => ({ ...connection, id: 'healthy-' + index, reconciliation: undefined, repository: { ...repository, id: 100 + index, fullName: 'acme/healthy-' + index }, imageRepository: 'ghcr.io/acme/healthy-' + index })), connection] }
const container = { id: 'web-id', name: 'app-web', project: 'Actual service', service: 'web', image: `${connection.imageRepository}:latest`, state: 'running', health: 'healthy' }
const projects = [{ name: 'Actual service', status: 'healthy', containers: [container] }, { name: 'Other', status: 'healthy', containers: [{ ...container, name: 'other', image: 'ghcr.io/everyonesoftware/finance-other:latest' }] }]
let reads = 0, approvals = 0, malformed = false, failure = '', statusFailure = false, held = null, planUnavailable = false, plans = 0
await page.route('**/api/**', async route => {
  const req = route.request(), path = new URL(req.url()).pathname
  if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
  if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'admin', username: 'Fixture', role: 'admin' } } })
  if (path === '/api/publishers' && req.method() === 'GET') { reads++; return statusFailure ? route.fulfill({ status: 503, body: 'Readback unavailable' }) : route.fulfill({ json: status }) }
  if (path.endsWith('/reconciliation/plan')) {
    assert.equal(req.method(), 'POST'); assert.deepEqual(req.postDataJSON(), {})
    plans++
    if (planUnavailable) return route.fulfill({ status: 503, body: 'Provider unavailable' })
    const plan = { repository: { ...repository }, imageRepository: connection.imageRepository, previousName: connection.reconciliation.previousName, fingerprint: 'a'.repeat(64), requiresApproval: true }
    if (malformed === 'approval') plan.requiresApproval = false
    if (malformed === 'identity') plan.repository.id++
    if (malformed === 'installation') plan.repository.installationId++
    if (malformed === 'scope') plan.imageRepository += '-other'
    if (malformed === 'name') plan.previousName = 'not a repository'
    if (malformed === 'fingerprint') plan.fingerprint = 'invalid'
    return route.fulfill({ json: plan })
  }
  if (path.endsWith('/reconciliation')) { assert.deepEqual(req.postDataJSON(), { fingerprint: 'a'.repeat(64), approved: true }); approvals++; if (held) await held; if (failure === 'partial') { connection.reconciliation.state = 'unavailable'; return route.fulfill({ status: 503, body: 'Saved reconciliation unavailable' }) } if (failure === 'network') return route.abort('failed'); if (failure === 'stale') return route.fulfill({ status: 409, body: 'Review is stale' }); if (failure === 'readback') statusFailure = true; connection.reconciliation.state = 'awaiting_publication'; return route.fulfill({ json: { connection } }) }
  if (path === '/api/projects') return route.fulfill({ json: { projects } })
  if (path === '/api/monitor') return route.fulfill({ json: { connected: true, lastChecked: new Date().toISOString(), incidents: [], events: [] } })
  if (path === '/api/operations') return route.fulfill({ json: { operations: [] } })
  if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
  return route.fulfill({ status: 404 })
})
const base = server.resolvedUrls.local[0]
const card = page.getByRole('article', { name: 'Publisher everyonesoftware/finance', exact: true })
const dialog = page.getByRole('dialog', { name: 'Review repository rename', exact: true })
try {
  await page.goto(base)
  const attention = page.getByRole('region', { name: 'Publisher attention' })
  await attention.getByText('Actual service', { exact: true }).waitFor()
  assert.equal(await attention.getByText('Other', { exact: true }).count(), 0)
  await page.locator('.project-group').filter({ hasText: 'Actual service' }).getByText('Healthy', { exact: true }).waitFor()
  const href = `#publishers/${encodeURIComponent(connection.id)}`
  assert.equal(await attention.getByRole('link', { name: 'Review reconciliation' }).getAttribute('href'), href)
  await page.getByRole('button', { name: 'Service filter: All services (2)', exact: true }).click()
  await page.getByRole('menuitemradio', { name: 'Needs attention (1)', exact: true }).click()
  assert.equal(await page.locator('.project-group').count(), 1, 'Publisher problems are included in attention filtering')
  await page.locator('.project-link').click()
  await page.getByRole('heading', { name: 'Actual service', exact: true }).waitFor()
  assert.equal(await page.locator('.service-detail .publisher-signal').getAttribute('href'), href)
  await page.locator('.container-link').click()
  await page.getByRole('heading', { name: 'app-web', exact: true }).waitFor()
  assert.equal(await page.locator('.container-page .publisher-signal').getAttribute('href'), href)
  await page.locator('.container-page .publisher-signal').click()
  await card.getByRole('button', { name: 'Review rename', exact: true }).waitFor()
  const reviewBounds = await card.getByRole('button', { name: 'Review rename', exact: true }).boundingBox()
  assert.ok(reviewBounds.y >= 0 && reviewBounds.y + reviewBounds.height < 900, 'Deep link places the primary warning action near the top, even with many publishers')
  assert.equal(await card.getByRole('button', { name: 'Review rename', exact: true }).count(), 1)
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 650 })
    const contentBounds = await page.locator('.app-content').boundingBox()
    for (const label of ['Review rename', 'Refresh status']) {
      const buttonBounds = await card.getByRole('button', { name: label, exact: true }).boundingBox()
      assert.ok(buttonBounds.y >= contentBounds.y && buttonBounds.y + buttonBounds.height <= contentBounds.y + contentBounds.height, `${label} is visible above mobile navigation without scrolling at ${width}px`)
    }
    assert.ok(await page.locator('.app-content').evaluate(el => el.scrollWidth <= el.clientWidth))
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
    if (process.env.RECONCILIATION_SCREENSHOTS) await page.screenshot({ path: `${process.env.RECONCILIATION_SCREENSHOTS}/notice-${width}.png` })
  }
  await page.setViewportSize({ width: 1280, height: 900 })
  await card.getByRole('tab', { name: 'Connection', exact: true }).waitFor()
  await card.getByRole('tab', { name: 'Latest image', exact: true }).waitFor()
  await card.getByRole('button', { name: 'Review rename', exact: true }).click()
  await dialog.getByText('everyonesoftware/finance-typescript', { exact: true }).waitFor()
  for (const viewport of [{ width: 700, height: 580 }, { width: 390, height: 650 }, { width: 320, height: 650 }]) {
    await page.setViewportSize(viewport)
    const bounds = await dialog.evaluate(el => { const b = el.getBoundingClientRect(); return { scroll: el.scrollHeight, client: el.clientHeight, width: el.scrollWidth, inner: el.clientWidth, top: b.top, bottom: b.bottom } })
    assert.ok(bounds.scroll <= bounds.client + 1, JSON.stringify({ viewport, bounds }))
    assert.ok(bounds.width <= bounds.inner + 1)
    assert.ok(bounds.top >= 0 && bounds.bottom <= viewport.height)
    for (const button of await dialog.getByRole('button').all()) { const box = await button.boundingBox(); assert.ok(box.y + box.height <= viewport.height && box.height >= 44) }
    if (process.env.RECONCILIATION_SCREENSHOTS) await page.screenshot({ path: process.env.RECONCILIATION_SCREENSHOTS + '/rename-' + viewport.width + '.png' })
  }
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.keyboard.press('Escape')
  await dialog.waitFor({ state: 'detached' })
  assert.equal(await card.getByRole('button', { name: 'Review rename', exact: true }).evaluate(el => el === document.activeElement), true, 'Cancel restores invoking action focus')
  const open = async () => { await card.getByRole('button', { name: 'Review rename', exact: true }).click(); await dialog.getByText('everyonesoftware/finance-typescript', { exact: true }).waitFor() }
  for (const mode of ['stale', 'network']) {
    failure = mode; await open(); await dialog.getByRole('checkbox').check(); const beforeFailure = reads
    await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
    await dialog.getByRole('alert').waitFor()
    assert.ok(reads > beforeFailure)
    assert.equal(await dialog.getByRole('button', { name: 'Apply', exact: true }).isDisabled(), true)
    assert.equal(await dialog.getByRole('checkbox').count(), 0, 'Discard failed approval')
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  }
  failure = ''
  for (const invalid of ['approval', 'identity', 'installation', 'scope', 'name', 'fingerprint']) {
    malformed = invalid
    await card.getByRole('button', { name: 'Review rename', exact: true }).click()
    await dialog.getByRole('alert').waitFor()
    assert.equal(await dialog.getByRole('button', { name: 'Apply', exact: true }).isDisabled(), true)
    assert.equal(await dialog.getByRole('checkbox').count(), 0)
    assert.equal(approvals, 2)
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  }
  malformed = false; await open()
  await page.setViewportSize({ width: 320, height: 360 })
  assert.ok(await dialog.evaluate(el => el.scrollHeight > el.clientHeight), 'Smaller/zoomed windows keep safe scrolling rather than clipping review')
  await dialog.getByRole('checkbox').check()
  await dialog.getByRole('checkbox').uncheck()
  for (let index = 0; index < 6; index++) {
    await page.keyboard.press('Tab')
    assert.equal(await dialog.evaluate(el => el.contains(document.activeElement)), true, 'Native modal contains keyboard focus')
  }
  await page.setViewportSize({ width: 1280, height: 900 })
  const apply = dialog.getByRole('button', { name: 'Apply', exact: true })
  assert.equal(await apply.isDisabled(), true)
  assert.equal(approvals, 2)
  await dialog.getByRole('checkbox').check()
  const before = reads
  let release
  held = new Promise(resolve => { release = resolve })
  const submitted = page.waitForRequest(request => new URL(request.url()).pathname.endsWith('/reconciliation'))
  await apply.evaluate(button => { button.click(); button.click() })
  await submitted
  for (const button of await dialog.getByRole('button').all()) assert.equal(await button.isDisabled(), true)
  await page.keyboard.press('Escape')
  assert.equal(await dialog.isVisible(), true, 'An uncertain in-flight mutation cannot be dismissed')
  release(); held = null
  await dialog.waitFor({ state: 'detached' })
  assert.equal(approvals, 3); assert.ok(reads > before)
  assert.equal(await card.locator('.publisher-rename-notice').evaluate(el => el === document.activeElement), true, 'Completed review focuses its saved-status notice when the invoking action disappears')
  await card.locator('.publisher-rename-notice').getByText('Awaiting next publication', { exact: true }).waitFor()
  assert.equal(await card.getByRole('button', { name: 'Review rename', exact: true }).count(), 0)
  await page.goto(`${base}#monitor`)
  await page.locator('.project-group').filter({ hasText: 'Actual service' }).getByText('Publisher awaiting publication', { exact: true }).waitFor()
  assert.equal(await attention.count(), 1, 'Delivery remains in attention until a real publication receipt')
  await attention.getByText('Publisher awaiting publication. Running containers are unaffected by publisher reconciliation.', { exact: true }).waitFor()
  assert.equal(await attention.getByRole('link', { name: 'Review reconciliation', exact: true }).count(), 0)
  const pendingLink = attention.getByRole('link', { name: 'View publisher status', exact: true })
  assert.equal(await pendingLink.getAttribute('href'), href)
  assert.ok(await page.locator('.publisher-attention-count').count() > 0)
  await page.locator('.project-group').filter({ hasText: 'Actual service' }).getByText('Healthy', { exact: true }).waitFor()
  await page.getByRole('button', { name: 'Service filter: All services (2)', exact: true }).click()
  await page.getByRole('menuitemradio', { name: 'Needs attention (1)', exact: true }).click()
  assert.equal(await page.locator('.project-group').count(), 1, 'Awaiting delivery remains in publisher-only attention filtering')
  await page.locator('.project-group').filter({ hasText: 'Actual service' }).waitFor()
  await pendingLink.click()
  await card.locator('.publisher-rename-notice').getByText('Awaiting next publication', { exact: true }).waitFor()
  await card.getByRole('button', { name: 'Refresh status', exact: true }).click()
  await card.locator('.publisher-rename-notice').getByText('Awaiting next publication', { exact: true }).waitFor()
  // A verified connectivity callback is not a publication receipt.
  assert.equal(connection.state, 'verified')
  connection.reconciliation.state = 'reconciled'
  connection.lastPublication = { version: '118', digest: 'sha256:' + 'c'.repeat(64), tags: ['118'], commit: '', runUrl: '', publishedAt: '2026-11-19T01:00:00Z', receivedAt: '2026-11-19T01:00:01Z', result: 'accepted' }
  await card.getByRole('button', { name: 'Refresh status', exact: true }).click()
  await card.getByText('Repository rename reconciled', { exact: true }).waitFor()
  await card.getByRole('tab', { name: 'Latest image', exact: true }).click()
  await card.getByRole('heading', { name: '118', exact: true }).waitFor()
  await page.goto(`${base}#monitor`)

  await page.locator('.project-group').first().waitFor()
  assert.equal(await attention.count(), 0)
  assert.equal(await page.locator('.publisher-attention-count').count(), 0, 'Publisher badges clear only after receipt reconciliation')
  await page.locator('.project-group').filter({ hasText: 'Actual service' }).getByText('Publisher rename reconciled', { exact: true }).waitFor()
  await page.locator('.project-group').filter({ hasText: 'Actual service' }).getByText('Healthy', { exact: true }).waitFor()
  await page.getByRole('button', { name: 'Service filter: All services (2)', exact: true }).click()
  await page.getByRole('menuitemradio', { name: 'Needs attention (0)', exact: true }).click()
  assert.equal(await page.locator('.project-group').count(), 0, 'Receipt reconciliation clears publisher-only attention filtering')
  connection.reconciliation = { ...reconciliation }
  await page.goto(`${base}#publishers/${encodeURIComponent(connection.id)}`)
  await card.getByRole('button', { name: 'Review rename', exact: true }).click()
  await dialog.getByText('everyonesoftware/finance-typescript', { exact: true }).waitFor()
  failure = 'readback'; await dialog.getByRole('checkbox').check()
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
  await dialog.getByRole('alert').filter({ hasText: 'Saved status could not be confirmed' }).waitFor()
  assert.equal(await dialog.isVisible(), true)
  assert.equal(await dialog.getByRole('button', { name: 'Apply', exact: true }).isDisabled(), true)
  statusFailure = false; failure = ''
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  for (const state of ['identity_mismatch', 'unavailable']) {
    connection.reconciliation.state = state
    await card.getByRole('button', { name: 'Refresh status', exact: true }).click()
    await card.getByText(state === 'identity_mismatch' ? 'Repository identity mismatch' : 'Repository identity unavailable', { exact: true }).waitFor()
    assert.equal(await card.getByRole('button', { name: 'Review rename', exact: true }).count(), 0)
  }
  // Nonempty legacy binding with an unchanged name is a real approval contract.
  connection.reconciliation = { ...reconciliation, previousName: repository.fullName }
  await card.getByRole('button', { name: 'Refresh status', exact: true }).click()
  await card.getByText('Repository identity review required', { exact: true }).waitFor()
  const identityDialog = page.getByRole('dialog', { name: 'Review repository identity', exact: true })
  await card.getByRole('button', { name: 'Review identity', exact: true }).click()
  await identityDialog.getByRole('checkbox').waitFor()
  assert.equal(await identityDialog.getByRole('button', { name: 'Apply', exact: true }).isDisabled(), true)
  failure = 'partial'
  const beforePartial = approvals, beforePartialReads = reads
  await identityDialog.getByRole('checkbox').check()
  await identityDialog.getByRole('button', { name: 'Apply', exact: true }).evaluate(button => { button.click(); button.click() })
  await identityDialog.getByRole('alert').waitFor()
  assert.equal(approvals, beforePartial + 1, 'Uncertain apply is never blindly duplicated')
  assert.ok(reads > beforePartialReads, 'Uncertain apply always reads saved state')
  assert.equal(await identityDialog.getByRole('checkbox').count(), 0)
  assert.equal(await identityDialog.getByRole('button', { name: 'Apply', exact: true }).isDisabled(), true)
  await identityDialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await card.getByText('Repository identity unavailable', { exact: true }).waitFor()
  assert.equal(await card.getByRole('button', { name: 'Review identity', exact: true }).evaluate(el => el === document.activeElement), true)
  // A provider outage may be retried explicitly, but cannot produce approval.
  planUnavailable = true
  await card.getByRole('button', { name: 'Review identity', exact: true }).click()
  await identityDialog.getByRole('alert').waitFor()
  assert.equal(await identityDialog.getByRole('checkbox').count(), 0)
  assert.equal(await identityDialog.getByRole('button', { name: 'Apply', exact: true }).isDisabled(), true)
  assert.equal(approvals, beforePartial + 1)
  await identityDialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  planUnavailable = false; failure = ''
  const beforeRecoveryPlans = plans
  await card.getByRole('button', { name: 'Review identity', exact: true }).click()
  await identityDialog.getByRole('checkbox').waitFor()
  assert.ok(plans > beforeRecoveryPlans, 'Recovery fetches a fresh plan with revalidated pending fingerprint')
  assert.equal(await identityDialog.getByRole('checkbox').isChecked(), false, 'Reopened review requires fresh consent')
  assert.equal(await identityDialog.getByRole('button', { name: 'Apply', exact: true }).isDisabled(), true)
  await identityDialog.getByRole('checkbox').check()
  await identityDialog.getByRole('button', { name: 'Apply', exact: true }).click()
  await identityDialog.waitFor({ state: 'detached' })
  assert.equal(approvals, beforePartial + 2)
  await card.locator('.publisher-rename-notice').getByText('Awaiting next publication', { exact: true }).waitFor()
  assert.equal(await card.getByRole('button', { name: 'Review identity', exact: true }).count(), 0)
  connection.reconciliation = { ...reconciliation }
  for (const viewport of [{ width: 700, height: 580 }, { width: 390, height: 650 }, { width: 320, height: 650 }]) {
    await page.setViewportSize(viewport)
    await page.goto(`${base}?reconciliationViewport=${viewport.width}#publishers/${encodeURIComponent(connection.id)}`)
    await card.getByRole('button', { name: 'Review rename', exact: true }).waitFor()
    const contentBounds = await page.locator('.app-content').boundingBox()
    for (const label of ['Review rename', 'Refresh status']) {
      const bounds = await card.getByRole('button', { name: label, exact: true }).boundingBox()
      assert.ok(bounds.y >= contentBounds.y && bounds.y + bounds.height <= contentBounds.y + contentBounds.height, `${label} is immediately reachable on a cold ${viewport.width}px deep link`)
    }
    assert.equal(await card.getByRole('button', { name: 'Review rename', exact: true }).count(), 1)
  }
  assert.deepEqual(errors, [])
  console.log('PASS publisher reconciliation: exact-image monitor attention/filter and overview links; desktop/phone deep links; explicit approval; same-name legacy identity review; unavailable partial-apply close/reopen recovery with fresh consent and exact fingerprint; provider-unavailable fail-closed retry; invalid/mismatched/stale/network/readback guards; single submission; keyboard/focus; pending-vs-receipt transitions; compact geometry and safe overflow. Isolated HTTP fixtures only.')
} finally { await browser.close(); await server.close() }
