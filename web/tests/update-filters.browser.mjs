// Browser-only API fixtures; never starts a real Docker update.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const page = await browser.newPage({ viewport: { width: 1200, height: 900 } })
const errors = []
page.on('pageerror', error => errors.push(error.message))
let operations = []
let updateAvailable = true
let submissions = 0
let rejectUpdate = false
const operation = { id: 'update-1', project: 'media', action: 'update', state: 'queued', queuedAt: new Date().toISOString(), messages: [] }
const wait = async predicate => { for (let i = 0; i < 150; i++) { if (await predicate()) return; await new Promise(resolve => setTimeout(resolve, 20)) } throw new Error('Timed out waiting for assertion') }
const filter = name => page.getByRole('menuitemradio', { name: new RegExp(`^${name}`), includeHidden: true })
const chooseFilter = async name => {
  await page.getByRole('button', { name: /^Service filter:/ }).click()
  await filter(name).click()
}
const refresh = async () => { await page.getByRole('button', { name: /Refresh/ }).click() }
await page.route('**/api/**', async route => {
  const path = new URL(route.request().url()).pathname
  if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
  if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture-admin', username: 'Browser fixture', role: 'admin' } } })
  if (path === '/api/projects/media/update') {
    submissions++
    return rejectUpdate ? route.fulfill({ status: 409, body: 'Fixture submission rejected' }) : route.fulfill({ json: operation })
  }
  if (path === '/api/projects') return route.fulfill({ json: { projects: [{ name: 'media', status: 'healthy', containers: [{ id: 'media-1', name: 'media-web-1', service: 'web', project: 'media', image: 'nginx:stable', state: 'running', health: 'healthy', updateAvailable }] }] } })
  if (path === '/api/monitor') return route.fulfill({ json: { connected: true, lastChecked: new Date().toISOString(), error: null, incidents: [], events: [], notificationConfigured: false } })
  if (path === '/api/operations') return route.fulfill({ json: { operations } })
  if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
  return route.fulfill({ status: 404 })
})
try {
  await page.goto(server.resolvedUrls.local[0])
  await chooseFilter('Updates')
  await page.locator('.group-summary').click()
  await page.getByRole('button', { name: /Review 1 update/ }).click()
  await page.getByRole('button', { name: 'Confirm & update', exact: true }).click()
  await page.locator('.update-dialog').waitFor({ state: 'detached' })
  // The operations poll intentionally still reports nothing: acceptance alone must move the project.
  await wait(() => page.locator('.project-group').count().then(count => count === 0))
  assert.match(await filter('Updates').textContent(), /Updates \(0\)/)
  assert.match(await filter('Needs attention').textContent(), /Needs attention \(1\)/)
  await chooseFilter('Needs attention')
  await page.locator('.group-signals .status-badge').getByText('Queued', { exact: true }).waitFor()
  assert.equal(await page.getByRole('button', { name: /Review .*update/ }).count(), 0)
  assert.equal(submissions, 1)
  operations = [{ ...operation, state: 'running' }]
  await refresh()
  await page.locator('.group-signals .status-badge').getByText('Updating', { exact: true }).waitFor()
  await page.reload()
  await chooseFilter('Needs attention')
  await page.locator('.group-signals .status-badge').getByText('Updating', { exact: true }).waitFor()
  await page.setViewportSize({ width: 390, height: 800 })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
  if (process.env.UPDATE_FILTER_SCREENSHOT) await page.screenshot({ path: process.env.UPDATE_FILTER_SCREENSHOT })
  // A failed update releases the gate and permits manual retry if an image is still available.
  operations = [{ ...operation, state: 'error', error: 'Fixture failure', needsAttention: true }]
  await refresh()
  await wait(() => filter('Updates').textContent().then(text => /Updates \(1\)/.test(text)))
  await chooseFilter('Updates')
  await page.locator('.group-summary').click()
  assert.equal(await page.getByRole('button', { name: /Review 1 update/ }).isEnabled(), true)
  rejectUpdate = true
  await page.getByRole('button', { name: /Review 1 update/ }).click()
  await page.getByRole('button', { name: 'Confirm & update', exact: true }).click()
  await page.getByRole('alert').getByText('Fixture submission rejected', { exact: true }).waitFor()
  assert.equal(await page.locator('.update-dialog').isVisible(), true)
  await page.getByRole('button', { name: 'Close', exact: true }).click()
  operations = [{ ...operation, state: 'complete' }]
  updateAvailable = false
  await refresh()
  await wait(() => filter('Updates').textContent().then(text => /Updates \(0\)/.test(text)))
  assert.match(await filter('Needs attention').textContent(), /Needs attention \(0\)/)
  await chooseFilter('All services')
  await page.locator('.group-signals .status-badge').getByText('Healthy', { exact: true }).waitFor()
  operations = []
  await refresh()
  assert.equal(submissions, 2)
  assert.deepEqual(errors, [])
  console.log('PASS: automatic close after acceptance despite stale polling, queued/running filters and counts, no duplicate update action, reload persistence, rejection stays visible, completion refresh, 390px fit; one accepted submission and one rejected retry.')
} finally { await browser.close(); await server.close() }
