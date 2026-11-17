// Real React app, browser-only API fixtures. No live Services or GitHub writes.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ ...(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : { channel: process.env.BROWSER_CHANNEL || 'msedge' }), headless: true })
const page = await browser.newPage({ viewport: { width: 1200, height: 1000 } })
const errors = [], calls = [], unexpected = []
let failure = '', hold = null, statusReads = 0
page.on('pageerror', error => errors.push(error.message))
const connection = (id, state) => ({ id, state, repository: { id: Number(id), fullName: `Demo/Publisher-${id}`, defaultBranch: 'main', installationId: 8 }, imageRepository: `ghcr.io/demo/publisher-${id}`, createdAt: '2026-01-01T00:00:00Z' })
const publication = { version: 'v3.2.1', tags: ['latest', 'v3.2.1'], digest: `sha256:${'a'.repeat(64)}`, commit: 'abcdef1234567890', runUrl: 'https://github.com/Demo/Publisher-1/actions/runs/123', publishedAt: '2026-11-10T08:00:00Z', receivedAt: '2026-11-10T08:01:00Z', result: 'accepted' }
const status = { available: true, github: { connected: true, name: 'Browser fixture App', permissionsChecked: true, variablesWrite: true }, tailnet: { clientId: '', audience: '', tags: '' }, connections: [{ ...connection('1', 'verified'), lastPublication: publication }, { ...connection('2', 'error'), error: 'Fixture delivery failed' }, { ...connection('3', 'disconnected'), remoteDisablePending: true }], endpoint: 'https://services.example.test/api/images/published' }
await page.route('**/api/**', async route => {
  const req = route.request(), path = new URL(req.url()).pathname
  if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
  if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture-admin', username: 'Browser fixture', role: 'admin' } } })
  if (path === '/api/publishers' && req.method() === 'GET') { statusReads++; return route.fulfill({ json: status }) }
  const mutation = path.match(/^\/api\/publishers\/(\d+)\/(test|rotate|disconnect|recover)$/)
  if (mutation) {
    assert.equal(req.method(), 'POST')
    assert.deepEqual(req.postDataJSON(), {})
    const [, id, action] = mutation
    calls.push(`${id}/${action}`)
    if (hold) await hold.promise
    if (failure === action) return route.fulfill({ status: 409, body: `Fixture ${action} failed` })
    const saved = status.connections.find(item => item.id === id)
    saved.state = action === 'disconnect' ? 'disconnected' : 'awaiting_callback'
    saved.remoteDisablePending = false
    delete saved.error
    return route.fulfill({ json: { connection: saved } })
  }
  if (path === '/api/projects') return route.fulfill({ json: { projects: [] } })
  if (path === '/api/monitor') return route.fulfill({ json: { connected: true, incidents: [], events: [] } })
  if (path === '/api/operations') return route.fulfill({ json: { operations: [] } })
  if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
  unexpected.push(`${req.method()} ${path}`)
  return route.fulfill({ status: 500, body: 'Unexpected fixture request' })
})
const row = id => page.getByRole('article', { name: `Publisher Demo/Publisher-${id}`, exact: true })
const summary = id => row(id).locator('.publisher-row-summary')
const tab = (id, name) => row(id).getByRole('tab', { name, exact: true })
const panel = id => row(id).getByRole('tabpanel')
const dialog = name => page.getByRole('dialog', { name, exact: true })
const waitUntil = async (fn, message) => {
  const deadline = Date.now() + 5000
  while (Date.now() < deadline) { if (await fn()) return; await new Promise(resolve => setTimeout(resolve, 25)) }
  assert.fail(message)
}
const expand = async (id, name) => {
  if (await summary(id).getAttribute('aria-expanded') !== 'true') await summary(id).click()
  if (name) await tab(id, name).click()
}
const refresh = async () => {
  const before = statusReads
  await page.getByRole('button', { name: 'Refresh', exact: true }).click()
  await waitUntil(() => statusReads > before, 'Refresh reads saved API state')
  await page.getByRole('button', { name: 'Refresh', exact: true }).waitFor()
  await waitUntil(async () => await page.getByRole('button', { name: 'Refresh', exact: true }).isEnabled(), 'Refresh completes')
}
const screenshot = async name => {
  if (!process.env.COMPACT_SCREENSHOT_DIR) return
  await mkdir(process.env.COMPACT_SCREENSHOT_DIR, { recursive: true })
  await page.screenshot({ path: join(process.env.COMPACT_SCREENSHOT_DIR, `${name}.png`), fullPage: true })
}
const layout = async label => {
  const measurements = await page.locator('.publisher-connection-row').evaluateAll(elements => elements.map(el => { const r = el.getBoundingClientRect(); return { top: r.top, bottom: r.bottom } }))
  for (let i = 1; i < measurements.length; i++) assert.ok(Math.abs(measurements[i].top - measurements[i - 1].bottom - 10) < 1, `${label}: adjacent rows have 10px gap`)
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${label}: no document overflow`)
  assert.ok(await row('1').evaluate(el => el.scrollWidth <= el.clientWidth), `${label}: no row overflow`)
  const identity = await summary('1').evaluate(el => {
    const name = el.querySelector('strong').getBoundingClientRect()
    const image = el.querySelector('code').getBoundingClientRect()
    return { nameBottom: name.bottom, imageTop: image.top }
  })
  assert.ok(identity.imageTop >= identity.nameBottom - 1, `${label}: repository name and image path occupy separate, non-overlapping lines`)
}
const assertPending = async id => {
  await row(id).getByText('Waiting for authenticated callback', { exact: true }).first().waitFor()
  assert.equal(await row(id).getByText('Connectivity verified', { exact: true }).count(), 0, 'accepted dispatch is not authenticated verification')
}
const confirmationCycle = async (action, buttonName, title, confirmLabel) => {
  await expand('1', 'Connection')
  const before = calls.length
  await row('1').getByRole('button', { name: buttonName, exact: true }).click()
  const modal = dialog(title)
  await modal.waitFor()
  assert.equal(await modal.getByRole('button', { name: 'Cancel', exact: true }).evaluate(el => el === document.activeElement), true, 'safe cancel receives focus')
  await modal.getByRole('button', { name: 'Cancel', exact: true }).click()
  assert.equal(calls.length, before, 'cancel never mutates')
  failure = action
  await row('1').getByRole('button', { name: buttonName, exact: true }).click()
  await modal.getByRole('button', { name: confirmLabel, exact: true }).click()
  await modal.getByRole('alert').getByText(`Fixture ${action} failed`, { exact: true }).waitFor()
  assert.equal(status.connections[0].state, 'verified', 'failed action preserves saved connection')
  await modal.getByRole('button', { name: 'Cancel', exact: true }).click()
  failure = ''
  hold = {}; hold.promise = new Promise(resolve => { hold.resolve = resolve })
  await row('1').getByRole('button', { name: buttonName, exact: true }).click()
  await modal.getByRole('button', { name: confirmLabel, exact: true }).click()
  await waitUntil(() => calls.length === before + 2, 'confirmed management request dispatched')
  assert.equal(await modal.getByRole('button', { name: 'Cancel', exact: true }).isDisabled(), true)
  assert.equal(await modal.getByRole('button', { name: 'Please wait…', exact: true }).isDisabled(), true)
  assert.equal(await page.getByRole('button', { name: 'Refresh', exact: true }).isDisabled(), true)
  assert.equal(await row('1').getByRole('button', { name: buttonName, exact: true }).isDisabled(), true)
  await page.keyboard.press('Escape')
  assert.equal(await modal.isVisible(), true, 'pending confirmation cannot be dismissed')
  hold.resolve(); hold = null
  await modal.waitFor({ state: 'detached' })
}
try {
  await page.goto(`${server.resolvedUrls.local[0]}#publishers`)
  await row('1').waitFor()
  assert.equal(await page.locator('.publisher-connection-row').count(), 3, 'three compact publisher rows replace expanded cards')
  assert.equal(await page.locator('.publisher-row-details').count(), 0, 'all rows initially collapsed')
  assert.equal(await page.locator('.publisher-row-summary[aria-expanded="false"]').count(), 3)
  await layout('desktop collapsed')
  await screenshot('desktop-collapsed')
  await summary('1').focus()
  await page.keyboard.press('Enter')
  await panel('1').waitFor()
  assert.equal(await summary('1').getAttribute('aria-expanded'), 'true')
  assert.equal(await summary('1').getAttribute('aria-controls'), await row('1').locator('.publisher-row-details').getAttribute('id'))
  assert.equal(await tab('1', 'Latest image').getAttribute('aria-selected'), 'true')
  assert.equal(await tab('1', 'Latest image').getAttribute('aria-controls'), await panel('1').getAttribute('id'))
  assert.equal(await panel('1').getAttribute('aria-labelledby'), await tab('1', 'Latest image').getAttribute('id'))
  assert.equal(await tab('1', 'Latest image').getAttribute('tabindex'), '0')
  assert.equal(await tab('1', 'Connection').getAttribute('tabindex'), '-1')
  assert.equal(await row('1').getByRole('tablist').count(), 1)
  assert.equal(await panel('1').getByRole('button', { name: /Test push|Rotate key|Disconnect/ }).count(), 0, 'Latest image contains no management actions')
  const workflow = panel('1').getByRole('link', { name: /workflow run/i })
  assert.equal(await workflow.getAttribute('href'), publication.runUrl)
  assert.equal(await workflow.getAttribute('target'), '_blank')
  assert.match(await workflow.getAttribute('rel'), /noreferrer|noopener/)
  assert.equal(await panel('1').getByText('v3.2.1', { exact: true }).count() >= 1, true)
  const disclosures = panel('1').locator('details')
  assert.ok(await disclosures.count() >= 1, 'publication metadata uses a disclosure')
  for (const disclosure of await disclosures.all()) {
    assert.equal(await disclosure.getAttribute('open'), null, 'metadata initially collapsed')
    await disclosure.locator('summary').click()
  }
  await panel('1').getByText(publication.digest, { exact: true }).waitFor()
  await panel('1').getByText(publication.commit, { exact: true }).waitFor()
  await panel('1').getByText('latest · v3.2.1', { exact: true }).waitFor()
  await panel('1').getByText(publication.result, { exact: true }).waitFor()
  for (const label of ['Published', 'Received']) {
    const fact = panel('1').locator('dl > div').filter({ has: page.locator('dt', { hasText: new RegExp(`^${label}$`) }) })
    assert.ok((await fact.locator('dd').innerText()).length > 0, `${label} timestamp present`)
    assert.doesNotMatch(await fact.locator('dd').innerText(), /Not available|Invalid Date/)
  }
  await layout('desktop expanded')
  await screenshot('desktop-latest-image')
  await tab('1', 'Latest image').focus()
  await page.keyboard.press('ArrowRight')
  assert.equal(await tab('1', 'Connection').evaluate(el => el === document.activeElement), true, 'arrow key moves tab focus')
  if (await tab('1', 'Connection').getAttribute('aria-selected') !== 'true') await page.keyboard.press('Enter')
  assert.equal(await tab('1', 'Connection').getAttribute('aria-selected'), 'true')
  for (const name of ['Test push', 'Rotate key', 'Disconnect']) assert.equal(await panel('1').getByRole('button', { name, exact: true }).isVisible(), true, `${name} is a direct action`)
  assert.equal(await row('1').getByRole('menu').count(), 0)
  assert.equal(await row('1').locator('[aria-haspopup="menu"]').count(), 0)
  await page.keyboard.press('Home')
  assert.equal(await tab('1', 'Latest image').evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('End')
  assert.equal(await tab('1', 'Connection').evaluate(el => el === document.activeElement), true)
  await summary('2').click()
  assert.equal(await page.locator('.publisher-row-details').count(), 1, 'exactly one row expands')
  assert.equal(await summary('1').getAttribute('aria-expanded'), 'false')
  assert.equal(await tab('2', 'Connection').getAttribute('aria-selected'), 'true', 'error defaults to Connection')
  await row('2').getByText('Fixture delivery failed', { exact: true }).waitFor()
  await summary('2').focus()
  await page.keyboard.press('Space')
  assert.equal(await page.locator('.publisher-row-details').count(), 0, 'Space collapses row')
  await page.setViewportSize({ width: 390, height: 800 })
  await layout('390px collapsed')
  await screenshot('mobile-collapsed')
  await expand('1', 'Latest image')
  await layout('390px expanded')
  await screenshot('mobile-latest-image')
  await expand('1', 'Connection')
  await screenshot('mobile-connection')
  await page.setViewportSize({ width: 1200, height: 1000 })

  await confirmationCycle('rotate', 'Rotate key', 'Rotate key for Demo/Publisher-1?', 'Rotate & test push')
  await assertPending('1')
  assert.equal(await row('1').getByRole('button', { name: 'Rotate key', exact: true }).count(), 0)
  status.connections[0].state = 'verified' // Simulate authenticated server callback, not dispatch acceptance.
  await refresh()
  await row('1').getByText('Connectivity verified', { exact: true }).first().waitFor()
  await confirmationCycle('disconnect', 'Disconnect', 'Disconnect Demo/Publisher-1?', 'Disconnect publisher')
  await row('1').getByText('Disconnected', { exact: true }).first().waitFor()
  assert.equal(await row('1').getByRole('button', { name: 'Reconnect', exact: true }).isVisible(), true)
  assert.equal(await row('1').getByRole('button', { name: 'Delete', exact: true }).isVisible(), true)
  assert.equal(await row('1').getByRole('button', { name: 'Test push', exact: true }).count(), 0)

  await expand('3')
  assert.equal(await tab('3', 'Connection').getAttribute('aria-selected'), 'true', 'disconnected defaults to Connection')
  for (const name of ['Reconnect', 'Delete', 'Retry GitHub cleanup']) assert.equal(await row('3').getByRole('button', { name, exact: true }).isVisible(), true)
  await row('3').getByRole('button', { name: 'Retry GitHub cleanup', exact: true }).click()
  await dialog('Disconnect Demo/Publisher-3?').getByRole('button', { name: 'Disconnect publisher', exact: true }).click()
  await dialog('Disconnect Demo/Publisher-3?').waitFor({ state: 'detached' })
  assert.equal(await row('3').getByRole('button', { name: 'Retry GitHub cleanup', exact: true }).count(), 0)

  await expand('2', 'Connection')
  await row('2').getByRole('button', { name: 'Repair & retry push', exact: true }).click()
  await waitUntil(() => calls.includes('2/recover'), 'error recovery uses /recover backend')
  const back = page.getByRole('button', { name: 'Back to publishers', exact: true })
  await expand('2', 'Connection')
  await assertPending('2')
  assert.equal(await back.count(), 0, 'overview recovery does not enter setup wizard')
  assert.equal(await row('2').getByText('Fixture delivery failed', { exact: true }).count(), 0)

  status.connections[0].state = 'verified'
  await refresh()
  await expand('1', 'Connection')
  await row('1').getByRole('button', { name: 'Test push', exact: true }).click()
  await waitUntil(() => calls.includes('1/test'), 'Test push uses /test backend')
  await expand('1', 'Connection')
  await assertPending('1')
  assert.equal(await back.count(), 0, 'overview test does not enter setup wizard')

  // Untrusted workflow URLs must never become actionable links.
  status.connections[0].state = 'verified'
  for (const unsafe of ['javascript:alert(1)', 'https://evil.example.test/Demo/Publisher-1/actions/runs/123', 'https://github.com/Other/Repository/actions/runs/123']) {
    status.connections[0].lastPublication = { ...publication, runUrl: unsafe }
    await refresh()
    await expand('1', 'Latest image')
    assert.equal(await panel('1').getByRole('link', { name: /workflow run/i }).count(), 0, `unsafe workflow URL omitted: ${unsafe}`)
  }
  status.connections[0].lastPublication = publication
  status.available = false
  await refresh()
  await expand('1', 'Connection')
  for (const name of ['Test push', 'Rotate key', 'Disconnect']) assert.equal(await row('1').getByRole('button', { name, exact: true }).isDisabled(), true, 'unavailable server disables mutations')
  status.available = true
  await refresh()
  await page.reload()
  await row('1').waitFor()
  assert.equal(await page.locator('.publisher-row-details').count(), 0, 'reload restores collapsed rows')
  await expand('1', 'Latest image')
  assert.equal(await panel('1').getByRole('link', { name: /workflow run/i }).getAttribute('href'), publication.runUrl, 'reload reads saved publication')
  assert.deepEqual(calls, ['1/rotate', '1/rotate', '1/disconnect', '1/disconnect', '3/disconnect', '2/recover', '1/test'])
  assert.deepEqual(errors, [])
  assert.deepEqual(unexpected, [])
  console.log('PASS compact publisher browser fixtures: collapse/exclusive expansion; 10px desktop/390px gaps; keyboard rows/tabs; publication metadata and safe links; direct actions; rotate/disconnect cancel/failure/success; busy/unavailable guards; backend test/recovery dispatch; pending is not verified; disconnected cleanup; refresh/reload; no page errors or live writes')
} finally { await browser.close(); await server.close() }
