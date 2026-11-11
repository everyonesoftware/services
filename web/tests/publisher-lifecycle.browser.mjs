// Browser-only fixtures: no live GitHub, Tailscale, or Services writes.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const page = await browser.newPage({ viewport: { width: 1200, height: 1000 } })
const errors = []
page.on('pageerror', error => errors.push(error.message))
const connection = (id, state) => ({ id, state, repository: { id: Number(id), fullName: `Demo/Publisher-${id}`, defaultBranch: 'main', installationId: 8 }, imageRepository: `ghcr.io/demo/publisher-${id}`, createdAt: '2026-01-01T00:00:00Z' })
const status = { available: true, github: { connected: true, name: 'Browser fixture App', permissionsChecked: true, variablesWrite: true }, tailnet: { clientId: '', audience: '', tags: '' }, connections: [connection('1', 'disconnected'), connection('2', 'verified'), connection('3', 'disconnected'), connection('4', 'error')], endpoint: 'https://services.example.test/api/images/published' }
const calls = []
let failRemove = false
let failReconnect = false
await page.route('**/api/**', async route => {
  const req = route.request(), path = new URL(req.url()).pathname
  if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
  if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture-admin', username: 'Browser fixture', role: 'admin' } } })
  if (path === '/api/publishers' && req.method() === 'GET') return route.fulfill({ json: status })
  if (path === '/api/publishers/1/reconnect') {
    assert.equal(req.method(), 'POST'); calls.push('reconnect')
    if (failReconnect) return route.fulfill({ status: 409, body: 'Fixture conflicting repository settings' })
    status.connections[0].state = 'awaiting_callback'
    return route.fulfill({ json: { connection: status.connections[0] } })
  }
  if (path === '/api/publishers/3') {
    assert.equal(req.method(), 'DELETE'); calls.push('remove')
    if (failRemove) return route.fulfill({ status: 409, body: 'Fixture cleanup is pending' })
    status.connections = status.connections.filter(c => c.id !== '3')
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
const order = async () => page.locator('.publisher-connections article h2').allTextContents()
try {
  await page.goto(`${server.resolvedUrls.local[0]}#publishers`)
  await card('1').waitFor()
  assert.deepEqual(await order(), ['Demo/Publisher-2', 'Demo/Publisher-4', 'Demo/Publisher-1', 'Demo/Publisher-3'])
  await card('1').getByRole('button', { name: 'Reconnect', exact: true }).click()
  await dialog().getByRole('heading', { name: 'Reconnect Demo/Publisher-1?' }).waitFor()
  assert.equal(await dialog().getByRole('button', { name: 'Cancel', exact: true }).evaluate(el => el === document.activeElement), true)
  await dialog().getByRole('button', { name: 'Cancel', exact: true }).click()
  assert.deepEqual(calls, [])
  failReconnect = true
  await card('1').getByRole('button', { name: 'Reconnect', exact: true }).click()
  await dialog().getByRole('button', { name: 'Reconnect & test push', exact: true }).click()
  await dialog().getByRole('alert').getByText('Fixture conflicting repository settings', { exact: true }).waitFor()
  assert.equal(await card('1').getByText('Disconnected', { exact: true }).count(), 1)
  await dialog().getByRole('button', { name: 'Cancel', exact: true }).click()
  failReconnect = false
  await card('1').getByRole('button', { name: 'Reconnect', exact: true }).click()
  await dialog().getByRole('button', { name: 'Reconnect & test push', exact: true }).click()
  await dialog().waitFor({ state: 'detached' })
  await card('1').getByText('Waiting for authenticated callback', { exact: true }).first().waitFor()
  assert.deepEqual(await order(), ['Demo/Publisher-1', 'Demo/Publisher-2', 'Demo/Publisher-4', 'Demo/Publisher-3'])
  await card('3').getByRole('button', { name: 'Remove', exact: true }).click()
  await dialog().getByRole('button', { name: 'Cancel', exact: true }).click()
  assert.equal(calls.includes('remove'), false)
  failRemove = true
  await card('3').getByRole('button', { name: 'Remove', exact: true }).click()
  await dialog().getByRole('button', { name: 'Remove publisher', exact: true }).click()
  await dialog().getByRole('alert').getByText('Fixture cleanup is pending', { exact: true }).waitFor()
  assert.equal(await card('3').count(), 1)
  await dialog().getByRole('button', { name: 'Cancel', exact: true }).click()
  await page.setViewportSize({ width: 390, height: 800 })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), '390px page must not overflow')
  await card('3').getByRole('button', { name: 'Remove', exact: true }).click()
  assert.ok(await dialog().evaluate(el => el.scrollWidth <= el.clientWidth), 'confirmation must not overflow')
  if (process.env.LIFECYCLE_SCREENSHOT) await page.screenshot({ path: process.env.LIFECYCLE_SCREENSHOT, fullPage: true })
  failRemove = false
  await dialog().getByRole('button', { name: 'Remove publisher', exact: true }).click()
  await card('3').waitFor({ state: 'detached' })
  assert.equal(await dialog().count(), 0)
  await page.reload()
  await card('1').waitFor()
  assert.equal(await card('3').count(), 0)
  assert.deepEqual(await order(), ['Demo/Publisher-1', 'Demo/Publisher-2', 'Demo/Publisher-4'])
  assert.deepEqual(calls, ['reconnect', 'reconnect', 'remove', 'remove'])
  assert.deepEqual(errors, [])
  console.log('PASS browser fixtures: stable ordering; reconnect and remove cancel/failure/success; refresh; mobile layout; no page errors')
} finally { await browser.close(); await server.close() }
