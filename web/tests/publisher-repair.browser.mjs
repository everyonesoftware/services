// Local fixtures only: never writes GitHub, Tailscale, or a live Services server.
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || '/opt/data/.local/bin/chromium', headless: true })
const page = await browser.newPage({ viewport: { width: 1280, height: 1050 } })
page.setDefaultTimeout(8000)
const errors = []; page.on('pageerror', error => errors.push(error.message))
const repository = { id: 14, fullName: 'everyonesoftware/finance-typescript', defaultBranch: 'main', installationId: 8 }
const freshRepository = { ...repository, fullName: 'everyonesoftware/finance-renamed', defaultBranch: 'release/next' }
const connection = { id: 'finance', repository, imageRepository: 'ghcr.io/everyonesoftware/finance-typescript', state: 'error', configured: false, credentialAvailable: false, error: 'Publisher credential is unavailable. Review configuration repair before retrying.', createdAt: '2026-11-11T05:20:56Z' }
const status = { available: true, github: { connected: true, permissionsChecked: true, variablesWrite: true }, tailnet: {}, connections: [connection], endpoint: 'https://services.example.ts.net/api/images/published' }
let review = 0, reads = 0, failure = '', planFailure = false, malformed = '', statusFailure = false, hold = null
const mutations = []
const desired = 'https://services.example.ts.net/api/images/published'
const fingerprint = () => createHash('sha256').update(`review-${review}`).digest('hex')
const plan = () => {
  const result = { repository: freshRepository, imageRepository: connection.imageRepository, configuration: { fingerprint: fingerprint(), secretExists: true, variables: [
  { name: 'SERVICES_PUBLISH_URL', exists: true, currentValue: 'https://old.example.ts.net/api/images/published', desiredValue: desired },
  { name: 'SERVICES_PUBLISH_NOTIFICATIONS', exists: true, currentValue: 'false', desiredValue: 'false' },
  { name: 'TAILSCALE_CLIENT_ID', exists: true, currentValue: 'finance-client', desiredValue: 'finance-client' },
  { name: 'TAILSCALE_AUDIENCE', exists: false, currentValue: '', desiredValue: 'api.tailscale.com/finance-client' },
  { name: 'TAILSCALE_TAGS', exists: true, currentValue: 'tag:services-publisher', desiredValue: 'tag:services-publisher' },
] } }
  const config = result.configuration
  if (malformed === 'null') config.variables = null
  if (malformed === 'empty') config.variables = []
  if (malformed === 'partial') config.variables = config.variables.slice(1)
  if (malformed === 'duplicate') config.variables[4] = { ...config.variables[0] }
  if (malformed === 'unknown') config.variables[4].name = 'UNKNOWN'
  if (malformed === 'notifications') config.variables[1].desiredValue = 'true'
  if (malformed.startsWith('fingerprint:')) config.fingerprint = malformed.slice('fingerprint:'.length)
  return result
}
await page.route('**/api/**', async route => {
  const req = route.request(), path = new URL(req.url()).pathname
  if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
  if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture-admin', username: 'Browser fixture', role: 'admin' } } })
  if (path === '/api/publishers' && req.method() === 'GET') { reads++; return statusFailure ? route.fulfill({ status: 503, body: 'Saved status unavailable' }) : route.fulfill({ json: status }) }
  if (path.endsWith('/repair/plan')) {
    assert.equal(req.method(), 'POST'); assert.deepEqual(req.postDataJSON(), {}); review++
    if (planFailure) return route.fulfill({ status: 503, body: 'Review unavailable' })
    return route.fulfill({ json: plan() })
  }
  if (path.endsWith('/repair')) {
    mutations.push(req.postDataJSON()); assert.deepEqual(req.postDataJSON(), { fingerprint: fingerprint() })
    if (hold) await hold
    if (failure === 'network') return route.abort('failed')
    if (failure === 'readback') statusFailure = true
    if (failure && failure !== 'readback') return route.fulfill({ status: 409, body: 'Review is stale; refresh review' })
    Object.assign(connection, { repository: freshRepository, state: 'awaiting_callback', configured: true, credentialAvailable: true, error: '' })
    return route.fulfill({ json: { connection } })
  }
  if (path === '/api/projects') return route.fulfill({ json: { projects: [] } })
  if (path === '/api/monitor') return route.fulfill({ json: { connected: true, incidents: [], events: [] } })
  if (path === '/api/operations') return route.fulfill({ json: { operations: [] } })
  if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
  throw new Error(`Unexpected ${req.method()} ${path}`)
})
const card = page.locator('.publisher-connection-row')
const dialog = page.getByRole('dialog', { name: 'Review publisher repair', exact: true })
const open = async () => { await card.getByRole('button', { name: 'Repair & retry push', exact: true }).click(); await dialog.getByText('Current value', { exact: true }).first().waitFor() }
const shots = process.env.REPAIR_SCREENSHOTS || '/opt/data/cache/scratch/services-publisher-repair'
await mkdir(shots, { recursive: true })
const screenshot = async name => { console.log(`Screenshot ${name}`); await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true, timeout: 10000 }) }
try {
  await page.goto(`${server.resolvedUrls.local[0]}#publishers`)
  await card.locator('.publisher-row-summary').click()
  assert.equal(await card.getByRole('button', { name: 'Rotate key' }).count(), 0)
  await screenshot('01-missing-credential')
  await open()
  assert.equal(mutations.length, 0)
  assert.deepEqual(connection.repository, repository, 'planning leaves saved stale metadata unchanged')
  await dialog.getByText(freshRepository.fullName, { exact: true }).waitFor()
  await dialog.getByText(freshRepository.defaultBranch, { exact: true }).waitFor()
  for (const text of ['Missing', 'Unchanged', 'Replace', 'https://old.example.ts.net/api/images/published', desired, 'Secret replacement warning']) await dialog.getByText(text, { exact: true }).first().waitFor()
  await screenshot('02-review-desktop')
  await dialog.evaluate(el => { el.scrollTop = el.scrollHeight })
  await screenshot('02b-review-desktop-warning')
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await open(); assert.equal(review, 2)
  failure = 'stale'
  const before = reads
  await dialog.getByRole('button', { name: 'Apply repair & test push', exact: true }).click()
  await dialog.getByRole('alert').filter({ hasText: 'Review is stale' }).waitFor()
  assert.ok(reads > before)
  assert.equal(await dialog.getByRole('button', { name: 'Apply repair & test push' }).isDisabled(), true)
  assert.equal(await dialog.getByText('old.example', { exact: false }).count(), 0)
  await screenshot('03-stale-review')
  planFailure = true
  await dialog.getByRole('button', { name: 'Refresh review', exact: true }).click()
  await dialog.getByRole('alert').filter({ hasText: 'Review unavailable' }).waitFor()
  planFailure = false
  const beforeInvalid = mutations.length
  for (const invalid of ['empty', 'partial', 'duplicate', 'unknown', 'null', 'notifications', ...['', 'review-arbitrary', 'a'.repeat(63), 'a'.repeat(65), 'A'.repeat(64), 'g'.repeat(64), `${'a'.repeat(64)}\n`].map(value => `fingerprint:${value}`)]) {
    malformed = invalid
    await dialog.getByRole('button', { name: 'Refresh review', exact: true }).click()
    await dialog.getByRole('alert').filter({ hasText: 'invalid' }).waitFor()
    assert.equal(await dialog.getByRole('button', { name: 'Apply repair & test push' }).isDisabled(), true, invalid)
    assert.equal(await dialog.getByText('Desired value', { exact: true }).count(), 0, invalid)
    assert.equal(mutations.length, beforeInvalid, invalid)
    console.log(`PASS fail-closed review: ${JSON.stringify(invalid)}`)
  }
  malformed = ''
  await dialog.getByRole('button', { name: 'Refresh review', exact: true }).click()
  await dialog.getByText('Current value', { exact: true }).first().waitFor()
  failure = 'network'; const previousReads = reads
  await dialog.getByRole('button', { name: 'Apply repair & test push', exact: true }).click()
  await dialog.getByRole('alert').waitFor()
  assert.ok(reads > previousReads)
  assert.equal(await dialog.getByRole('button', { name: 'Apply repair & test push' }).isDisabled(), true)
  failure = ''; await dialog.getByRole('button', { name: 'Refresh review', exact: true }).click()
  await dialog.getByText('Current value', { exact: true }).first().waitFor()
  await page.setViewportSize({ width: 390, height: 844 })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
  assert.ok(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth))
  await screenshot('04-review-mobile')
  await dialog.evaluate(el => { el.scrollTop = el.scrollHeight })
  await screenshot('04b-review-mobile-warning')
  let release; hold = new Promise(resolve => { release = resolve })
  await dialog.getByRole('button', { name: 'Apply repair & test push', exact: true }).click()
  await page.waitForTimeout(100)
  for (const button of await dialog.getByRole('button').all()) assert.equal(await button.isDisabled(), true)
  await page.keyboard.press('Escape'); assert.equal(await dialog.isVisible(), true)
  const savedReadback = page.waitForResponse(response => new URL(response.url()).pathname === '/api/publishers' && response.request().method() === 'GET')
  release(); hold = null
  assert.deepEqual((await (await savedReadback).json()).connections[0].repository, freshRepository, 'HTTP saved-state readback contains the fresh name and branch')
  await dialog.waitFor({ state: 'detached' })
  await card.getByText('Waiting for authenticated callback', { exact: true }).first().waitFor()
  assert.equal(await card.getByRole('button', { name: 'Repair & retry push' }).count(), 0)
  assert.equal(await card.getByRole('button', { name: 'Rotate key' }).count(), 0)
  await screenshot('05-awaiting-callback')
  Object.assign(connection, { state: 'verified', verifiedAt: '2026-11-17T12:00:00Z' })
  await card.getByText('Connectivity verified', { exact: true }).first().waitFor({ timeout: 10000 })
  await page.getByRole('article', { name: `Publisher ${freshRepository.fullName}`, exact: true }).waitFor()
  assert.deepEqual(connection.repository, freshRepository, 'saved readback exposes fresh name and branch after approval')
  await screenshot('06-verified')
  // An accepted repair without saved-state readback is never presented as success.
  Object.assign(connection, { state: 'disconnected', configured: false, credentialAvailable: false })
  await page.getByRole('button', { name: 'Refresh', exact: true }).click()
  await card.getByText('Disconnected', { exact: true }).first().waitFor()
  await open()
  failure = 'readback'
  await dialog.getByRole('button', { name: 'Apply repair & test push', exact: true }).click()
  await dialog.getByRole('alert').filter({ hasText: 'Saved status could not be confirmed' }).waitFor()
  assert.equal(await dialog.isVisible(), true)
  assert.equal(await dialog.getByRole('button', { name: 'Apply repair & test push' }).isDisabled(), true)
  await screenshot('07-unconfirmed-readback')
  statusFailure = false; failure = ''
  await dialog.getByRole('button', { name: 'Refresh review', exact: true }).click()
  await dialog.getByText('Repair is unavailable while awaiting', { exact: false }).waitFor()
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  connection.state = 'error'
  connection.deletion = { tailscaleRevocationAttested: true }
  await page.getByRole('button', { name: 'Refresh', exact: true }).click()
  await card.getByText('Manual Tailscale confirmation is saved.', { exact: false }).waitFor()
  assert.equal(await card.getByRole('button', { name: 'Repair & retry push' }).count(), 0)
  assert.equal(await card.getByRole('button', { name: 'Rotate key' }).count(), 0)
  assert.deepEqual(errors, [])
  console.log(`PASS reviewed repair: missing credential → fresh review → stale/network failure → refresh → locked approval → awaiting callback → verified; malformed plan; cancel; mobile overflow. Screenshots: ${shots}`)
} finally { await browser.close(); await server.close() }
