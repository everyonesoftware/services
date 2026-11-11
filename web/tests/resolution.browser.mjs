// Real React + browser-only API fixtures. Never connects to Docker or a live Services API.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const page = await browser.newPage({ viewport: { width: 1200, height: 900 } })
const errors = [], writes = [], unexpected = []
page.on('pageerror', error => errors.push(error.message))
// Test-only transport deliberately ignores AbortSignal and holds old reads.
await page.addInitScript(() => {
  const original = window.fetch.bind(window)
  window.__heldResolutionReads = []
  window.fetch = async (input, options = {}) => {
    const path = new URL(typeof input === 'string' ? input : input.url, location.href).pathname
    if (window.__holdResolutionReads && ['/api/monitor', '/api/projects'].includes(path)) {
      const gate = new Promise(resolve => window.__heldResolutionReads.push(resolve))
      const response = await original(input, { ...options, signal: undefined })
      await gate
      return response
    }
    return original(input, options)
  }
})
const since = '2026-11-11T12:06:17Z'
const missing = n => ({ id: `docker-old-${n}`, name: `old-${n}`, project: 'agent', service: `old-${n}`, image: `image-${n}:latest`, state: 'missing', monitorStatus: 'missing', monitorReason: 'Previously observed container disappeared' })
const current = n => ({ ...missing(n), id: `docker-new-${n}`, name: `new-${n}`, service: `new-${n}`, state: 'running', monitorStatus: 'healthy', health: 'healthy' })
const incident = n => ({ id: `agent/old-${n}/old-${n}`, project: 'agent', name: `old-${n}`, service: `old-${n}`, status: 'missing', reason: 'Previously observed container disappeared', since })
let incidents = [incident(1), incident(2)], containers = [missing(1), missing(2), current(1), current(2)]
let saved, fail = false, hold = null, connected = true, stale = false, monitorReads = 0, projectReads = 0, undoFailure = false
await page.route('**/api/**', async route => {
  const req = route.request(), path = new URL(req.url()).pathname
  if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
  if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture-admin', username: 'Browser fixture', role: 'admin' } } })
  if (path === '/api/monitor/resolve') {
    const body = req.postDataJSON(); writes.push({ path, body })
    assert.equal(req.method(), 'POST')
    if (hold) await hold
    if (fail) return route.fulfill({ status: 409, body: 'Fixture incident changed. Review and retry.' })
    saved = structuredClone({ incidents, containers })
    for (const decision of body.decisions) {
      assert.equal(decision.since, since)
      const old = incidents.find(i => i.id === decision.incidentId)
      assert.ok(old)
      assert.equal(decision.snoozedUntil, old.snoozedUntil, 'submit exact current snooze version, not a synthesized incident')
      if (decision.action === 'snooze') {
        old.snoozedUntil = new Date(Date.now() + decision.snoozeSeconds * 1000).toISOString()
        containers.find(c => c.name === old.name).monitorSnoozedUntil = old.snoozedUntil
      } else {
        incidents = incidents.filter(i => i.id !== old.id)
        containers = containers.filter(c => c.name !== old.name)
      }
    }
    return route.fulfill({ json: { undoToken: 'fixture-token' } })
  }
  if (path === '/api/monitor/undo') {
    writes.push({ path, body: req.postDataJSON() })
    assert.deepEqual(req.postDataJSON(), { token: 'fixture-token' })
    if (undoFailure) return route.fulfill({ status: 409, body: 'Fixture Undo unavailable' })
    ;({ incidents, containers } = structuredClone(saved))
    return route.fulfill({ status: 204 })
  }
  if (path === '/api/projects') { projectReads++; return route.fulfill({ json: { projects: [{ name: 'agent', status: incidents.length ? 'missing' : 'healthy', containers }] } }) }
  if (path === '/api/monitor') { monitorReads++; return route.fulfill({ json: { connected, lastChecked: new Date(Date.now() - (stale ? 60000 : 0)).toISOString(), error: null, incidents, events: [{ id: 'real-fixture-event', project: 'agent', name: 'old-1', kind: 'problem', message: 'Fixture observed disappearance', time: since }], notificationConfigured: false } }) }
  if (path === '/api/operations') return route.fulfill({ json: { operations: [] } })
  if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
  unexpected.push(`${req.method()} ${path}`)
  return route.fulfill({ status: 500, body: 'Unexpected test fixture request' })
})
const wait = async predicate => { for (let i = 0; i < 200; i++) { if (await predicate()) return; await new Promise(r => setTimeout(r, 20)) } assert.fail('Timed out waiting for browser assertion') }
const dialog = page.locator('.resolution-dialog')
const entry = n => dialog.getByRole('region', { name: `old-${n}`, exact: true })
const action = n => entry(n).getByLabel('How should Services handle this?')
const apply = () => dialog.getByRole('button', { name: /^Apply (resolution|\d+ decisions)$/ })
const single = n => page.locator('.incident').filter({ hasText: `old-${n}` }).getByRole('button', { name: 'Resolve…', exact: true })
const bulk = () => page.getByRole('button', { name: /^Resolve (both|all) missing entries…$/ })
const refresh = async () => { const before = monitorReads; await page.getByRole('button', { name: /Refresh/ }).click(); await wait(() => monitorReads > before) }
const undo = async () => { await page.getByRole('button', { name: 'Undo', exact: true }).click(); await wait(() => page.locator('.incident').count().then(n => n === 2)) }
const fit = async label => {
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), `${label}: document horizontal fit`)
  assert.ok(await page.locator('.app-content').evaluate(el => el.scrollWidth <= el.clientWidth), `${label}: dashboard behind modal fits horizontally`)
  assert.ok(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth), `${label}: dialog horizontal fit`)
  const before = await dialog.locator('.dialog-actions').boundingBox()
  const headerBefore = await dialog.locator('.dialog-header').boundingBox()
  await dialog.locator('.resolution-body').evaluate(el => { el.scrollTop = el.scrollHeight })
  assert.deepEqual(await dialog.locator('.dialog-actions').boundingBox(), before, `${label}: footer stationary`)
  assert.deepEqual(await dialog.locator('.dialog-header').boundingBox(), headerBefore, `${label}: header stationary`)
  assert.ok(before.y >= 0 && before.y + before.height <= (await page.viewportSize()).height, `${label}: footer visible`)
  if (process.env.RESOLUTION_SCREENSHOT_DIR) {
    await mkdir(process.env.RESOLUTION_SCREENSHOT_DIR, { recursive: true })
    await page.screenshot({ path: join(process.env.RESOLUTION_SCREENSHOT_DIR, `${label}.png`) })
  }
}
try {
  await page.goto(server.resolvedUrls.local[0])
  await single(1).waitFor({ timeout: 5000 })
  assert.equal(await page.getByRole('button', { name: /Investigate/ }).count(), 0)
  await single(1).click()
  assert.equal(await dialog.getByRole('heading', { level: 2 }).textContent(), 'Resolve missing entry')
  assert.equal(await apply().isDisabled(), true)
  await entry(1).getByText('Observation details', { exact: true }).click()
  await entry(1).getByText('Fixture observed disappearance', { exact: true }).waitFor()
  assert.match(await entry(1).textContent(), /image-1:latest/)
  assert.match(await entry(1).textContent(), /Possible replacement: new-1/)
  await action(1).selectOption('forget')
  await page.keyboard.press('Escape')
  await dialog.waitFor({ state: 'detached' })
  assert.equal(writes.length, 0)
  assert.equal(await single(1).evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Enter')
  assert.equal(await action(1).inputValue(), '', 'cancel discards only that dialog draft')
  await action(1).selectOption('replace')
  const firstFocusable = entry(1).locator('summary')
  await firstFocusable.focus()
  await page.keyboard.press('Shift+Tab')
  assert.equal(await apply().evaluate(el => el === document.activeElement), true, 'reverse Tab stays in modal')
  await page.keyboard.press('Tab')
  assert.equal(await firstFocusable.evaluate(el => el === document.activeElement), true, 'forward Tab wraps inside modal')
  assert.equal(await entry(1).getByLabel('Replaced by').inputValue(), 'docker-new-1')
  fail = true
  await apply().click()
  await dialog.getByRole('alert').getByText('Fixture incident changed. Review and retry.', { exact: true }).waitFor()
  assert.equal(await action(1).inputValue(), 'replace')
  assert.equal(await entry(1).getByLabel('Replaced by').inputValue(), 'docker-new-1')
  fail = false
  let release; hold = new Promise(r => { release = r })
  await apply().click()
  await wait(() => writes.length === 2)
  assert.equal(await dialog.locator('button:enabled,select:enabled,input:enabled').count(), 0)
  await page.keyboard.press('Escape')
  assert.equal(await dialog.count(), 1)
  const beforeReads = [monitorReads, projectReads]
  release(); hold = null
  await dialog.waitFor({ state: 'detached' })
  await wait(() => incidents.length === 1 && monitorReads > beforeReads[0] && projectReads > beforeReads[1])
  assert.deepEqual(writes.at(-1).body, { decisions: [{ incidentId: incident(1).id, since, action: 'replace', replacementId: 'docker-new-1' }] })
  undoFailure = true
  await page.getByRole('button', { name: 'Undo', exact: true }).click()
  await page.getByRole('alert').getByText('Fixture Undo unavailable', { exact: true }).waitFor()
  undoFailure = false
  await undo()
  await bulk().click()
  await entry(2).getByRole('checkbox').uncheck()
  await dialog.getByLabel('Apply to all selected').selectOption('forget')
  await entry(2).getByRole('checkbox').check()
  assert.equal(await action(2).inputValue(), '')
  assert.equal(await apply().isDisabled(), true)
  await dialog.getByLabel('Apply to all selected').selectOption('replace')
  await entry(2).getByLabel('Replaced by').selectOption('docker-new-1')
  assert.equal(await apply().isDisabled(), true)
  assert.equal(await dialog.getByText('Choose a different replacement for each old service.', { exact: true }).count(), 2)
  await action(2).selectOption('snooze')
  await entry(2).getByLabel('Pause alerts for').selectOption('900')
  await fit('desktop-bulk')
  await page.setViewportSize({ width: 390, height: 700 })
  await fit('phone-bulk')
  await page.setViewportSize({ width: 390, height: 520 })
  await fit('short-phone-bulk')
  await dialog.locator('.resolution-body').evaluate(el => { el.scrollTop = 0 })
  fail = true
  await apply().click()
  const bulkError = dialog.getByText('Fixture incident changed. Review and retry.', { exact: true })
  await bulkError.waitFor()
  assert.ok(await bulkError.evaluate(el => { const r = el.getBoundingClientRect(); return r.top >= 0 && r.bottom <= innerHeight }), 'bulk server error remains visible without scrolling away from the draft')
  assert.equal(await action(2).inputValue(), 'snooze')
  fail = false
  await apply().click()
  await dialog.waitFor({ state: 'detached' })
  await wait(() => page.locator('.incident').count().then(n => n === 1))
  assert.deepEqual(writes.at(-1).body.decisions, [{ incidentId: incident(1).id, since, action: 'replace', replacementId: 'docker-new-1' }, { incidentId: incident(2).id, since, action: 'snooze', snoozeSeconds: 900 }])
  assert.equal(await page.locator('.health-banner').count(), 0, 'Routine health summaries stay omitted')
  assert.equal(await page.locator('#incident-heading').textContent(), 'Missing · alerts snoozed 1', 'snoozed list count is labeled separately from active attention')
  await page.getByRole('button', { name: /^Service filter:/ }).click()
  assert.match(await page.getByRole('menuitemradio', { name: /^Needs attention/ }).textContent(), /\(0\)/)
  await page.getByRole('menuitemradio', { name: /^Needs attention/ }).click()
  await page.getByRole('heading', { name: 'No matching services', exact: true }).waitFor()
  await page.getByRole('button', { name: /^Service filter:/ }).click()
  await page.getByRole('menuitemradio', { name: /^All services/ }).click()
  await single(2).click()
  assert.match(await entry(2).textContent(), /Snoozed/)
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  const expectedSnoozedUntil = incidents[0].snoozedUntil
  if (!await page.locator('.project-group').evaluate(el => el.open)) await page.locator('.group-summary').click()
  const rowResolve = page.locator('.container-detail').filter({ hasText: 'old-2' }).getByRole('button', { name: 'Resolve…', exact: true })
  await rowResolve.click()
  assert.equal(await page.locator('.container-detail').filter({ hasText: 'old-2' }).evaluate(el => el.open), false, 'row Resolve does not toggle inspection')
  assert.match(await entry(2).textContent(), /Snoozed/)
  await action(2).selectOption('forget')
  await apply().click()
  await dialog.waitFor({ state: 'detached' })
  await wait(() => page.locator('.incident').count().then(n => n === 0))
  assert.deepEqual(writes.at(-1).body.decisions, [{ incidentId: incident(2).id, since, action: 'forget', snoozedUntil: expectedSnoozedUntil }])
  await page.getByRole('button', { name: 'Undo', exact: true }).click()
  await wait(() => page.locator('.incident').count().then(n => n === 1))
  assert.equal(incidents[0].snoozedUntil, expectedSnoozedUntil, 'Undo restores actual snoozed record')
  // Reset browser-only fixtures for the remaining independent bulk cases.
  incidents = [incident(1), incident(2)]; containers = [missing(1), missing(2), current(1), current(2)]
  await page.reload()
  await bulk().waitFor()
  await bulk().click()
  await entry(2).getByRole('checkbox').uncheck()
  await dialog.getByLabel('Apply to all selected').selectOption('forget')
  await apply().click()
  await dialog.waitFor({ state: 'detached' })
  await wait(() => page.locator('.incident').count().then(n => n === 1))
  assert.deepEqual(writes.at(-1).body.decisions, [{ incidentId: incident(1).id, since, action: 'forget' }])
  assert.equal(incidents[0].id, incident(2).id, 'excluded incident unchanged')
  await undo()
  connected = false; await refresh()
  await wait(() => single(1).isDisabled())
  connected = true; stale = true; await refresh()
  await wait(() => single(1).isDisabled())
  stale = false; await refresh()
  await wait(() => single(1).isEnabled())
  incidents.push(incident(3)); containers.push(missing(3), current(3))
  await refresh()
  await page.getByRole('button', { name: 'Resolve all missing entries…', exact: true }).waitFor()
  await page.evaluate(() => { window.__holdResolutionReads = true })
  await refresh()
  await wait(() => page.evaluate(() => window.__heldResolutionReads.length === 2))
  await page.evaluate(() => { window.__holdResolutionReads = false })
  await bulk().click()
  await dialog.getByLabel('Apply to all selected').selectOption('forget')
  await apply().click()
  await dialog.waitFor({ state: 'detached' })
  await wait(() => page.locator('.incident').count().then(n => n === 0))
  assert.equal(writes.at(-1).body.decisions.length, 3)
  await page.evaluate(() => window.__heldResolutionReads.forEach(release => release()))
  await page.waitForTimeout(150)
  assert.equal(await page.locator('.incident').count(), 0, 'old responses ignoring abort cannot resurrect resolved incidents')
  await page.getByRole('button', { name: /^Service filter:/ }).click()
  assert.match(await page.getByRole('menuitemradio', { name: /^Needs attention/ }).textContent(), /\(0\)/)
  await page.keyboard.press('Escape')
  assert.deepEqual(errors, [])
  assert.deepEqual(unexpected, [])
  console.log('PASS: single/bulk/mixed and excluded scope; evidence; cancel/Escape/focus; drafts preserved on error; pending locks; server Undo/retry; snooze counts/filter; disconnected/stale gate; desktop/390px/short-phone stationary footer and overflow. Browser fixtures only.')
} finally { await browser.close(); await server.close() }
