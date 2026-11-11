// Browser-only inventory reconciliation fixtures; no Docker mutations.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const page = await browser.newPage({ viewport: { width: 1200, height: 900 } })
const errors = []
page.on('pageerror', error => errors.push(error.message))
let operations = []
let hold = false
let submissions = 0
const pending = []
const project = version => ({ name: 'site', status: 'healthy', containers: [{ id: 'web', name: 'site-web-1', project: 'site', service: 'web', image: 'nginx:stable', state: 'running', health: 'healthy', updateAvailable: !!version, updateStatus: version ? `Available ${version}` : 'Current', updatePublication: version ? { schemaVersion: 1, version, publishedAt: '2026-11-09T12:34:56Z' } : undefined }] })
const fulfill = (route, version) => route.fulfill({ json: { projects: [project(version)] } }).catch(() => {})
const wait = async predicate => { for (let i = 0; i < 200; i++) { if (await predicate()) return; await new Promise(resolve => setTimeout(resolve, 20)) } throw new Error('Timed out waiting for assertion') }
const refresh = () => page.getByRole('button', { name: /^↻ Refresh$/ }).click()
const updates = () => page.getByRole('button', { name: /^Updates/ }).textContent()
await page.route('**/api/**', async route => {
  const path = new URL(route.request().url()).pathname
  if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
  if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture-admin', username: 'Inventory fixture', role: 'admin' } } })
  if (path === '/api/projects/site/update') { submissions++; return route.fulfill({ status: 409, body: 'Unexpected update submission' }) }
  if (path === '/api/projects') { if (hold) { pending.push(route); return } return fulfill(route, 'v2-stale') }
  if (path === '/api/operations') return route.fulfill({ json: { operations } })
  if (path === '/api/monitor') return route.fulfill({ json: { connected: true, lastChecked: new Date().toISOString(), error: null, incidents: [], events: [], notificationConfigured: false } })
  if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
  return route.fulfill({ status: 404 })
})
try {
  for (const observedState of ['complete', 'error', 'queued', 'running']) {
    const state = observedState === 'error' ? 'error' : 'complete'
    hold = false; pending.length = 0
    operations = []
    await page.goto(server.resolvedUrls.local[0])
    await page.locator('.group-summary').click()
    await page.getByRole('button', { name: /Review 1 update/ }).click()
    assert.match(await page.locator('.update-dialog').textContent(), /v2-stale/)
    assert.equal(await page.getByRole('button', { name: 'Confirm & update', exact: true }).isEnabled(), true)
    hold = true
    // Start inventory before the terminal observation, then keep both reads pending.
    // Dispatch the normal Refresh action behind the modal without dismissing review.
    await page.getByRole('button', { name: /^↻ Refresh$/ }).evaluate(button => button.click())
    await wait(() => pending.length >= 1)
    operations = [{ id: `operation-${observedState}`, project: 'site', action: 'update', state: observedState, queuedAt: new Date().toISOString(), messages: [] }]
    if (observedState === 'queued' || observedState === 'running') {
      await page.locator('.group-signals').getByText(observedState === 'queued' ? 'Queued' : 'Updating', { exact: true }).waitFor()
      assert.equal(await page.locator('.update-dialog').count(), 0, `${observedState} invalidates an already-open review`)
      if (observedState === 'queued') {
        operations = [{ ...operations[0], state: 'running' }]
        await page.locator('.group-signals').getByText('Updating', { exact: true }).waitFor()
      }
      operations = [{ ...operations[0], state }]
    }
    await wait(() => pending.length >= 2) // next operations poll triggers the post-observation read
    await page.locator('.group-signals').getByText('Refreshing', { exact: true }).waitFor()
    assert.equal(await page.locator('.update-dialog').count(), 0, 'terminal observation must invalidate the stale modal while inventory is pending')
    assert.equal(await page.getByRole('button', { name: /^Confirm &/ }).count(), 0)
    assert.equal(await updates(), 'Updates0')
    assert.equal(await page.locator('.group-update, .publication-details').count(), 0)
    await fulfill(pending.shift(), 'v2-stale')
    assert.equal(await updates(), 'Updates0', 'pre-observation response cannot release the gate')
    await pending.shift().fulfill({ status: 503, body: 'Inventory fixture unavailable' }).catch(() => {})
    await page.getByText('Inventory fixture unavailable', { exact: true }).waitFor()
    assert.equal(await updates(), 'Updates0')
    await page.locator('.group-signals').getByText('Refreshing', { exact: true }).waitFor()
    assert.equal(await page.locator('.update-dialog, .publication-details').count(), 0, 'failed inventory cannot restore stale review metadata')
    assert.equal(await page.getByRole('button', { name: /^Confirm &/ }).count(), 0)
    await refresh()
    await wait(() => pending.length >= 1)
    await fulfill(pending.shift(), 'v3-genuinely-newer')
    await wait(() => updates().then(text => text === 'Updates1'))
    if (!await page.locator('.project-group').evaluate(node => node.open)) await page.locator('.group-summary').click()
    await page.getByRole('button', { name: /Review 1 update/ }).waitFor()
    assert.equal(await page.getByRole('button', { name: /Review 1 update/ }).isEnabled(), true)
    assert.match(await page.locator('.publication-details').textContent(), /v3-genuinely-newer/)
    assert.equal(await page.locator('.update-dialog').count(), 0, 'fresh inventory must not silently reopen or replace the approved target')
    await page.getByRole('button', { name: /Review 1 update/ }).click()
    assert.match(await page.locator('.update-dialog').textContent(), /v3-genuinely-newer/)
    assert.doesNotMatch(await page.locator('.update-dialog').textContent(), /v2-stale/)
    assert.equal(await page.getByRole('button', { name: 'Confirm & update', exact: true }).isEnabled(), true)
    await page.getByRole('button', { name: 'Cancel', exact: true }).click()
    assert.equal(await page.getByText('Inventory fixture unavailable', { exact: true }).count(), 0)
    await page.setViewportSize({ width: 390, height: 800 })
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
    await page.setViewportSize({ width: 1200, height: 900 })
  }
  assert.deepEqual(errors, [])
  assert.equal(submissions, 0, 'external operation changes and inventory refreshes never authorize an update')
  console.log('PASS: open reviews invalidated for external queued/running/complete/error transitions, delayed pre/post-observation reads, pending/failed inventory hides stale counts/actions/publications, fresh newer updates require a new review, cancel, 390px fit; zero update submissions or Docker mutations.')
} finally { await browser.close(); await server.close() }
